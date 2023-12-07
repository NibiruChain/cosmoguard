package firewall

import (
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"

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
	cache            *ttlcache.Cache[string, CachedResponse]
	endpointHandlers []*httpProxyEndpointHandler
	mu               sync.RWMutex
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

	var cacheOptions []ttlcache.Option[string, CachedResponse]
	if cfg.CacheConfig != nil {
		cacheOptions = append(cacheOptions, ttlcache.WithTTL[string, CachedResponse](cfg.CacheConfig.TTL))
		if !cfg.CacheConfig.TouchOnHit {
			cacheOptions = append(cacheOptions, ttlcache.WithDisableTouchOnHit[string, CachedResponse]())
		}
	} else {
		cacheOptions = append(cacheOptions, ttlcache.WithTTL[string, CachedResponse](defaultCacheTTL))
	}
	proxy.cache = ttlcache.New[string, CachedResponse](cacheOptions...)

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

func (p *HttpProxy) Start() error {
	if p.responseTimeHist != nil {
		prometheus.MustRegister(p.responseTimeHist)
	}
	for _, eh := range p.endpointHandlers {
		if err := eh.Handler.Start(p.log); err != nil {
			return err
		}
	}
	p.log.WithField("address", p.server.Addr).Infof("starting http proxy")
	go p.cache.Start()
	return p.server.ListenAndServe()
}

func (p *HttpProxy) SetRules(rules []*HttpRule, defaultAction RuleAction) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules = rules
	p.defaultAction = defaultAction
}

func (p *HttpProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	p.mu.RLock()
	defer p.mu.RUnlock()
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
	if p.defaultAction == RuleActionAllow {
		p.allow(w, r, nil, start)
	} else {
		p.deny(w, r, start)
	}
}

func (p *HttpProxy) allow(w http.ResponseWriter, r *http.Request, cache *RuleCache, startTime time.Time) {
	if cache != nil && cache.Enable {
		hash, err := p.getHash(r)
		if err != nil {
			// We could not get the hash, but we can still try to serve the request
			p.log.Errorf("error getting hash of request: %v", err)
		} else {
			if p.cache.Has(hash) {
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
	}).Info("request allowed")
}

func (p *HttpProxy) getHash(req *http.Request) (string, error) {
	b, err := io.ReadAll(req.Body)
	if err != nil {
		return "", err
	}
	return util.Sha256(req.Method + req.URL.String() + string(b)), nil
}

func (p *HttpProxy) cacheHit(w http.ResponseWriter, r *http.Request, requestHash string, startTime time.Time) {
	item := p.cache.Get(requestHash)
	w.Header().Add("Cache", cacheHit)
	w.WriteHeader(item.Value().StatusCode)
	w.Write(item.Value().Data)
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
	}).Info("request allowed")

	b, err := ww.GetWrittenBytes()
	if err != nil {
		p.log.Errorf("error getting data from upstream response: %v", err)
		return
	}
	p.cache.Set(requestHash, CachedResponse{
		Data:       b,
		StatusCode: ww.GetStatusCode(),
	}, cache.TTL)
}

func (p *HttpProxy) deny(w http.ResponseWriter, r *http.Request, startTime time.Time) {
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte("unauthorized"))
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
	}).Info("request denied")
}
