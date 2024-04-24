package firewall

import (
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"

	"github.com/NibiruChain/cosmos-firewall/pkg/cache"
	"github.com/NibiruChain/cosmos-firewall/pkg/util"
)

type EndpointHandler interface {
	ServeHTTP(w http.ResponseWriter, r *http.Request, next func(w http.ResponseWriter, r *http.Request))
	Start(logger *log.Entry) error
}

type httpProxyEndpointHandler struct {
	Endpoints []Endpoint
	Handler   EndpointHandler
}

type Endpoint struct {
	Path   string
	Method string
}

type HttpProxy struct {
	defaultAction    RuleAction
	rules            []*HttpRule
	server           *http.Server
	proxy            *httputil.ReverseProxy
	cache            cache.Cache[string, CachedResponse]
	endpointHandlers []*httpProxyEndpointHandler
	rulesMutex       sync.RWMutex
	log              *log.Entry
	responseTimeHist *prometheus.HistogramVec
}

type CachedResponse struct {
	Data       []byte
	StatusCode int
}

func NewHttpProxy(name, localAddr, remoteAddr string, opts ...Option[HttpProxyOptions]) (*HttpProxy, error) {
	cfg := DefaultHttpProxyOptions()
	for _, opt := range opts {
		opt(cfg)
	}

	remoteURL, err := url.Parse(remoteAddr)
	if err != nil {
		return nil, err
	}
	proxy := HttpProxy{
		log:              log.WithField("proxy", name),
		server:           &http.Server{Addr: localAddr},
		proxy:            httputil.NewSingleHostReverseProxy(remoteURL),
		endpointHandlers: cfg.EndpointHandlers,
	}
	proxy.server.Handler = &proxy

	// Setup cache
	var cacheOptions []cache.Option
	if cfg.CacheConfig != nil {
		cacheOptions = append(cacheOptions, cache.DefaultTTL(cfg.CacheConfig.TTL))
	}

	if cfg.CacheConfig != nil && cfg.CacheConfig.Redis != nil {
		proxy.cache, err = cache.NewRedisCache[string, CachedResponse](*cfg.CacheConfig.Redis, cacheOptions...)
		if err != nil {
			return nil, err
		}
	} else {
		proxy.cache, err = cache.NewMemoryCache[string, CachedResponse](cacheOptions...)
		if err != nil {
			return nil, err
		}
	}

	if cfg.MetricsEnabled {
		proxy.responseTimeHist = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: name,
			Name:      "request_duration_seconds",
			Help:      "Histogram of response time for handler in seconds",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "status_code", "cache", "firewall"})
	}

	return &proxy, nil
}

func (p *HttpProxy) Run() error {
	if p.responseTimeHist != nil {
		prometheus.MustRegister(p.responseTimeHist)
	}

	for _, eh := range p.endpointHandlers {
		if err := eh.Handler.Start(p.log); err != nil {
			return err
		}
	}

	p.log.WithField("address", p.server.Addr).Infof("starting http proxy")
	return p.server.ListenAndServe()
}

func (p *HttpProxy) SetRules(rules []*HttpRule, defaultAction RuleAction) {
	p.rulesMutex.Lock()
	defer p.rulesMutex.Unlock()
	p.rules = rules
	p.defaultAction = defaultAction
}

func (p *HttpProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// If there are configured handlers for specific paths/methods lets
	// run them instead
	for _, handler := range p.endpointHandlers {
		for _, e := range handler.Endpoints {
			if e.Method == r.Method && e.Path == r.URL.Path {
				handler.Handler.ServeHTTP(w, r, p.proxy.ServeHTTP)
				return
			}
		}
	}

	start := time.Now()
	r.Body = ReusableReader(r.Body)

	// Check if there are matching rules
	p.rulesMutex.RLock()
	defer p.rulesMutex.RUnlock()
	for _, rule := range p.rules {
		match := rule.Match(r)
		if match {
			switch rule.Action {
			case RuleActionAllow:
				p.allow(w, r, rule.Cache, start)
				return

			case RuleActionDeny:
				p.deny(w, r, start)
				return

			default:
				p.log.Errorf("unrecognized rule action %q", rule.Action)
			}
		}
	}

	// Proceed with default action if there are no matches
	if p.defaultAction == RuleActionAllow {
		p.allow(w, r, nil, start)
	} else {
		p.deny(w, r, start)
	}
}

func (p *HttpProxy) allow(w http.ResponseWriter, r *http.Request, cache *RuleCache, startTime time.Time) {
	if cache != nil && cache.Enable {
		hash, err := p.getRequestHash(r)
		if err != nil {
			// We could not get the hash, but we can still serve the request
			p.log.Errorf("error getting hash of request: %v", err)
		} else {
			cached, err := p.cache.Has(r.Context(), hash)
			if err != nil {
				p.log.Errorf("error getting cached value: %v", err)
			}
			if cached {
				p.cacheHit(w, r, hash, startTime)
				return
			} else {
				p.cacheMiss(w, r, hash, cache, startTime)
				return
			}
		}

	}
	ww := WrapResponseWriter(w)
	p.proxy.ServeHTTP(ww, r)

	duration := time.Since(startTime)
	if p.responseTimeHist != nil {
		p.responseTimeHist.WithLabelValues(
			r.Method,
			strconv.Itoa(ww.GetStatusCode()),
			cacheMiss,
			RuleActionAllow).Observe(duration.Seconds())
	}

	p.log.WithFields(map[string]interface{}{
		"path":     r.URL.Path,
		"method":   r.Method,
		"duration": duration,
		"source":   GetSourceIP(r),
	}).Info("request allowed")
}

func (p *HttpProxy) getRequestHash(req *http.Request) (string, error) {
	b, err := io.ReadAll(req.Body)
	if err != nil {
		return "", err
	}
	return util.Sha256(req.Method + req.URL.String() + string(b)), nil
}

func (p *HttpProxy) cacheHit(w http.ResponseWriter, r *http.Request, requestHash string, startTime time.Time) {
	res, err := p.cache.Get(r.Context(), requestHash)
	if err != nil {
		p.log.Errorf("cache error: %v", err)
		return
	}

	WriteData(w, res.StatusCode, res.Data, cacheKey, cacheHit)

	duration := time.Since(startTime)
	if p.responseTimeHist != nil {
		p.responseTimeHist.WithLabelValues(
			r.Method,
			strconv.Itoa(http.StatusOK),
			cacheHit,
			RuleActionAllow).Observe(duration.Seconds())
	}

	p.log.WithFields(map[string]interface{}{
		"path":     r.URL.Path,
		"method":   r.Method,
		"cache":    cacheHit,
		"duration": duration,
		"source":   GetSourceIP(r),
	}).Info("request allowed")
}

func (p *HttpProxy) cacheMiss(w http.ResponseWriter, r *http.Request, requestHash string, cache *RuleCache, startTime time.Time) {
	w.Header().Add("Cache", cacheMiss)

	ww := WrapResponseWriter(w)
	p.proxy.ServeHTTP(ww, r)

	duration := time.Since(startTime)
	if p.responseTimeHist != nil {
		p.responseTimeHist.WithLabelValues(
			r.Method,
			strconv.Itoa(ww.GetStatusCode()),
			cacheMiss,
			RuleActionAllow).Observe(duration.Seconds())
	}

	p.log.WithFields(map[string]interface{}{
		"path":     r.URL.Path,
		"method":   r.Method,
		"cache":    cacheMiss,
		"duration": duration,
		"source":   GetSourceIP(r),
	}).Info("request allowed")

	b, err := ww.GetWrittenBytes()
	if err != nil {
		p.log.Errorf("error loading upstream response: %v\n response not cached", err)
		return
	}

	if ww.GetStatusCode() > 0 {
		err = p.cache.Set(r.Context(), requestHash, CachedResponse{
			Data:       b,
			StatusCode: ww.GetStatusCode(),
		}, cache.TTL)
		if err != nil {
			p.log.Errorf("error setting cache value: %v", err)
		}
	}
}

func (p *HttpProxy) deny(w http.ResponseWriter, r *http.Request, startTime time.Time) {
	WriteError(w, http.StatusUnauthorized, "unauthorized")

	duration := time.Since(startTime)
	if p.responseTimeHist != nil {
		p.responseTimeHist.WithLabelValues(
			r.Method,
			strconv.Itoa(http.StatusUnauthorized),
			cacheMiss,
			RuleActionDeny).Observe(duration.Seconds())
	}

	p.log.WithFields(map[string]interface{}{
		"path":     r.URL.Path,
		"method":   r.Method,
		"duration": duration,
		"source":   GetSourceIP(r),
	}).Info("request denied")
}
