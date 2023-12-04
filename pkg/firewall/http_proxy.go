package firewall

import (
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"

	"github.com/jellydator/ttlcache/v3"
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

	return &proxy, nil
}

func (p *HttpProxy) Start() error {
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
	r.Body = ReusableReader(r.Body)

	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, rule := range p.rules {
		match := rule.Match(r)
		if match {
			switch rule.Action {
			case RuleActionAllow:
				p.allow(w, r, rule.Cache)
				return

			case RuleActionDeny:
				p.deny(w, r)
				return

			default:
				p.log.Errorf("unrecognized rule action %q", rule.Action)
			}
		}
	}
	if p.defaultAction == RuleActionAllow {
		p.allow(w, r, nil)
	} else {
		p.deny(w, r)
	}
}

func (p *HttpProxy) allow(w http.ResponseWriter, r *http.Request, cache *RuleCache) {
	if cache != nil && cache.Enable {
		hash, err := p.getHash(r)
		if err != nil {
			// We could not get the hash, but we can still try to serve the request
			p.log.Errorf("error getting hash of request: %v", err)
			p.proxy.ServeHTTP(w, r)
			return
		}
		if p.cache.Has(hash) {
			p.log.WithFields(map[string]interface{}{
				"path":   r.URL.Path,
				"method": r.Method,
				"cache":  "hit",
			}).Info("request allowed")
			p.cacheHit(w, r, hash)
			return
		} else {
			p.log.WithFields(map[string]interface{}{
				"path":   r.URL.Path,
				"method": r.Method,
				"cache":  "miss",
			}).Info("request allowed")
			p.cacheMiss(w, r, hash, cache)
			return
		}
	}
	p.log.WithFields(map[string]interface{}{
		"path":   r.URL.Path,
		"method": r.Method,
	}).Info("request allowed")
	p.proxy.ServeHTTP(w, r)
}

func (p *HttpProxy) getHash(req *http.Request) (string, error) {
	b, err := io.ReadAll(req.Body)
	if err != nil {
		return "", err
	}
	return util.Sha256(req.Method + req.URL.String() + string(b)), nil
}

func (p *HttpProxy) cacheHit(w http.ResponseWriter, r *http.Request, requestHash string) {
	item := p.cache.Get(requestHash)
	w.Header().Add("Cache", "hit")
	w.WriteHeader(item.Value().StatusCode)
	w.Write(item.Value().Data)
}

func (p *HttpProxy) cacheMiss(w http.ResponseWriter, r *http.Request, requestHash string, cache *RuleCache) {
	w.Header().Add("Cache", "miss")
	ww := WrapResponseWriter(w)
	p.proxy.ServeHTTP(ww, r)

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

func (p *HttpProxy) deny(w http.ResponseWriter, r *http.Request) {
	p.log.WithFields(map[string]interface{}{
		"path":   r.URL.Path,
		"method": r.Method,
	}).Info("request denied")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte("unauthorized"))
}
