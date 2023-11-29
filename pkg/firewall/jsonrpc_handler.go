package firewall

import (
	"bytes"
	"fmt"
	"hash/maphash"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
)

type JsonRpcHandler struct {
	cache         *ttlcache.Cache[uint64, *JsonRpcMsg]
	defaultAction RuleAction
	wsProxy       *JsonRpcWebSocketProxy
	rules         []*JsonRpcRule
	mu            sync.RWMutex
	hash          *maphash.Hash
}

func NewJsonRpcHandler(opts ...Option[JsonRpcHandlerOptions]) (*JsonRpcHandler, error) {
	cfg := DefaultJsonRpcHandlerOptions()
	for _, opt := range opts {
		opt(cfg)
	}
	handler := &JsonRpcHandler{
		hash: &maphash.Hash{},
	}

	var cacheOptions []ttlcache.Option[uint64, *JsonRpcMsg]
	if cfg.CacheConfig != nil {
		cacheOptions = append(cacheOptions, ttlcache.WithTTL[uint64, *JsonRpcMsg](cfg.CacheConfig.TTL))
		if cfg.CacheConfig.DisableTouchOnHit {
			cacheOptions = append(cacheOptions, ttlcache.WithDisableTouchOnHit[uint64, *JsonRpcMsg]())
		}
	} else {
		cacheOptions = append(cacheOptions, ttlcache.WithTTL[uint64, *JsonRpcMsg](defaultCacheTTL))
	}
	handler.cache = ttlcache.New[uint64, *JsonRpcMsg](cacheOptions...)

	if cfg.WebsocketEnabled {
		var err error
		handler.wsProxy, err = NewJsonRpcWebSocketProxy(cfg.WebsocketBackend, cfg.WebsocketConnections, handler.cache)
		if err != nil {
			return nil, err
		}
	}

	return handler, nil
}

func (h *JsonRpcHandler) Start() error {
	go h.cache.Start()
	if h.wsProxy != nil {
		go func() {
			if err := h.wsProxy.Start(); err != nil {
				log.Errorf("error on websocket proxy: %v", err)
			}
		}()
	}
	return nil
}

func (h *JsonRpcHandler) SetRules(rules []*JsonRpcRule, defaultAction RuleAction) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rules = rules
	h.defaultAction = defaultAction
	h.wsProxy.SetRules(rules, defaultAction)
}

func (h *JsonRpcHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request)) {
	log.Info("serving http jsonrpc request")
	if r.Method == http.MethodPost && r.URL.Path == "/" {
		h.handleHttp(w, r, next)
		return
	}

	if h.wsProxy != nil && r.URL.Path == websocketPath && r.Method == http.MethodGet {
		log.Info("handling jsonrpc websocket connection")
		h.wsProxy.HandleConnection(w, r)
		return
	}

	w.WriteHeader(http.StatusBadRequest)
	w.Write([]byte("unexpected request"))
}

func (h *JsonRpcHandler) handleHttp(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request)) {
	var err error
	r.Body = ReusableReader(r.Body)

	log.Info("Cache:", h.cache.Len())

	// Get jsonrpc requests from body
	b, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
		return
	}

	req, requests, _ := ParseJsonRpcMessage(b)
	if req != nil {
		h.handleHttpSingle(req, w, r, next)
	} else {
		h.handleHttpBatch(requests, w, r, next)
	}
}

func (h *JsonRpcHandler) handleHttpSingle(request *JsonRpcMsg, w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request)) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, rule := range h.rules {
		hash := request.Hash()
		match := rule.Match(request)
		if match {
			switch rule.Action {
			case RuleActionAllow:
				log.Info("request allowed")

				if rule.Cache != nil {
					if h.cache.Has(hash) {
						log.Info("cache hit")
						h.writeSingleResponse(w, h.cache.Get(hash).Value())
						return
					}
					log.Info("cache miss")
					h.getSingleUpstreamResponse(w, r, next, hash, rule.Cache)
					return
				}
				next(w, r)
				return

			case RuleActionDeny:
				log.Info("request denied")
				h.writeSingleResponse(w, UnauthorizedResponse(request))
				return

			default:
				log.Errorf("unrecognized rule action %q", rule.Action)
			}
		}
	}
	if h.defaultAction == RuleActionAllow {
		next(w, r)
	} else {
		h.writeSingleResponse(w, UnauthorizedResponse(request))
	}
}

func (h *JsonRpcHandler) getSingleUpstreamResponse(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request), hash uint64, cache *RuleCache) {
	ww := WrapResponseWriter(w)
	next(ww, r)

	b, err := ww.GetWrittenBytes()
	if err != nil {
		log.Errorf("error getting data from upstream response: %v", err)
		return
	}
	res, _, _ := ParseJsonRpcMessage(b)
	h.cache.Set(hash, res, cache.TTL)
}

func (h *JsonRpcHandler) writeSingleResponse(w http.ResponseWriter, res *JsonRpcMsg) {
	b, err := res.Marshal()
	if err != nil {
		log.Errorf("error marshalling response from cache: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

func (h *JsonRpcHandler) handleHttpBatch(requests JsonRpcMsgs, w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request)) {
	var responses JsonRpcResponses

	h.mu.RLock()
	for _, req := range requests {
		for _, rule := range h.rules {
			match := rule.Match(req)
			if match {
				switch rule.Action {
				case RuleActionAllow:
					log.Info("request allowed")
					responses.AddPendingOrLoadFromCache(req, h.cache, rule.Cache, req.Hash())

				case RuleActionDeny:
					log.Info("request denied")
					responses.Deny(req)

				default:
					log.Errorf("unrecognized rule action %q", rule.Action)
				}
				break
			}
		}
	}
	h.mu.RUnlock()

	// send pending requests to upstream and grab the response
	pendingRequests := responses.GetPendingRequests()
	if len(pendingRequests) > 0 {
		log.Info("getting from upstream")
		upstreamResponses, err := h.getResponsesFromUpstream(r, responses.GetPendingRequests(), next)
		if err != nil {
			log.Errorf("error getting responses from upstream: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		responses.Set(responses.GetPendingRequests(), upstreamResponses)
		responses.StoreInCache(h.cache)
	}

	b, err := responses.GetFinal().Marshal()
	if err != nil {
		log.Errorf("error marshalling response: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

func (h *JsonRpcHandler) getResponsesFromUpstream(httpRequest *http.Request, requests JsonRpcMsgs, next func(http.ResponseWriter, *http.Request)) (JsonRpcMsgs, error) {
	b, err := requests.Marshal()
	if err != nil {
		return nil, fmt.Errorf("error marshalling requests to upstream: %v", err)
	}
	req := httpRequest.Clone(httpRequest.Context())
	req.Body = io.NopCloser(bytes.NewReader(b))
	req.ContentLength = int64(len(b))

	w := httptest.NewRecorder()
	next(w, req)
	res := w.Result()

	b, err = io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading body from upstream response: %v", err)
	}
	_, responses, _ := ParseJsonRpcMessage(b)
	return responses, nil
}
