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
	Start() error
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
}

type CachedResponse struct {
	Data       []byte
	StatusCode int
}

func NewHttpProxy(localAddr, remoteAddr string, opts ...Option[HttpProxyOptions]) (*HttpProxy, error) {
	cfg := DefaultHttpProxyOptions()
	for _, opt := range opts {
		opt(cfg)
	}

	remoteURL, err := url.Parse(remoteAddr)
	if err != nil {
		return nil, err
	}
	proxy := HttpProxy{
		server:           &http.Server{Addr: localAddr},
		proxy:            httputil.NewSingleHostReverseProxy(remoteURL),
		endpointHandlers: cfg.EndpointHandlers,
	}
	proxy.server.Handler = &proxy

	var cacheOptions []ttlcache.Option[string, CachedResponse]
	if cfg.CacheConfig != nil {
		cacheOptions = append(cacheOptions, ttlcache.WithTTL[string, CachedResponse](cfg.CacheConfig.TTL))
		if cfg.CacheConfig.DisableTouchOnHit {
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
		if err := eh.Handler.Start(); err != nil {
			return err
		}
	}
	log.Infof("starting http proxy at %v", p.server.Addr)
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
				log.Errorf("unrecognized rule action %q", rule.Action)
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
	log.Info("request allowed")
	if cache != nil && cache.Enable {
		hash, err := p.getHash(r)
		if err != nil {
			// We could not get the hash, but we can still try to serve the request
			log.Errorf("error getting hash of request: %v", err)
			p.proxy.ServeHTTP(w, r)
			return
		}
		if p.cache.Has(hash) {
			p.cacheHit(w, r, hash)
			return
		} else {
			p.cacheMiss(w, r, hash, cache)
			return
		}
	}
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
	log.Info("cache hit!")
	item := p.cache.Get(requestHash)
	w.Header().Add("Cache", "hit")
	w.WriteHeader(item.Value().StatusCode)
	w.Write(item.Value().Data)
}

func (p *HttpProxy) cacheMiss(w http.ResponseWriter, r *http.Request, requestHash string, cache *RuleCache) {
	log.Info("cache miss")
	w.Header().Add("Cache", "miss")
	ww := WrapResponseWriter(w)
	p.proxy.ServeHTTP(ww, r)

	b, err := ww.GetWrittenBytes()
	if err != nil {
		log.Errorf("error getting data from upstream response: %v", err)
		return
	}
	p.cache.Set(requestHash, CachedResponse{
		Data:       b,
		StatusCode: ww.GetStatusCode(),
	}, cache.TTL)
}

func (p *HttpProxy) deny(w http.ResponseWriter, r *http.Request) {
	log.Info("request denied")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte("unauthorized"))
}
