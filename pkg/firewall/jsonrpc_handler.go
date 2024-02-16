package firewall

import (
	"bytes"
	"fmt"
	"hash/maphash"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

type JsonRpcHandler struct {
	cache            *ttlcache.Cache[uint64, *JsonRpcMsg]
	defaultAction    RuleAction
	wsProxy          *JsonRpcWebSocketProxy
	rules            []*JsonRpcRule
	mu               sync.RWMutex
	hash             *maphash.Hash
	log              *log.Entry
	responseTimeHist *prometheus.HistogramVec
	batchResTimeHist *prometheus.HistogramVec
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
		if !cfg.CacheConfig.TouchOnHit {
			cacheOptions = append(cacheOptions, ttlcache.WithDisableTouchOnHit[uint64, *JsonRpcMsg]())
		}
	} else {
		cacheOptions = append(cacheOptions, ttlcache.WithTTL[uint64, *JsonRpcMsg](defaultCacheTTL))
	}
	handler.cache = ttlcache.New[uint64, *JsonRpcMsg](cacheOptions...)

	if cfg.WebsocketEnabled {
		handler.wsProxy = NewJsonRpcWebSocketProxy(
			cfg.WebsocketBackend,
			cfg.WebsocketConnections,
			handler.cache,
			cfg.MetricsEnabled,
		)
	}

	if cfg.MetricsEnabled {
		handler.responseTimeHist = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "jsonrpc",
			Name:      "request_duration_seconds",
			Help:      "Histogram of response time for handler in seconds",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "path", "cache", "firewall"})
		handler.batchResTimeHist = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "jsonrpc_batch",
			Name:      "request_duration_seconds",
			Help:      "Histogram of response time for handler in seconds",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"requests", "allowed", "denied", "cache_hits", "cache_misses"})
	}

	return handler, nil
}

func (h *JsonRpcHandler) Start(logger *log.Entry) error {
	h.log = logger.WithField("handler", "jsonrpc")

	if h.responseTimeHist != nil {
		prometheus.MustRegister(h.responseTimeHist)
	}
	if h.batchResTimeHist != nil {
		prometheus.MustRegister(h.batchResTimeHist)
	}

	go h.cache.Start()
	if h.wsProxy != nil {
		go func() {
			if err := h.wsProxy.Start(h.log); err != nil {
				h.log.Errorf("error on websocket proxy: %v", err)
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
	start := time.Now()
	h.log.Debug("serving http jsonrpc request")
	if r.Method == http.MethodPost && r.URL.Path == "/" {
		h.handleHttp(w, r, next, start)
		return
	}

	if h.wsProxy != nil && r.URL.Path == websocketPath && r.Method == http.MethodGet {
		h.log.Debug("handling jsonrpc websocket connection")
		h.wsProxy.HandleConnection(w, r)
		return
	}

	w.WriteHeader(http.StatusBadRequest)
	w.Write([]byte("unexpected request"))
}

func (h *JsonRpcHandler) handleHttp(w http.ResponseWriter, r *http.Request,
	next func(http.ResponseWriter, *http.Request), startTime time.Time) {
	var err error
	r.Body = ReusableReader(r.Body)

	// Get jsonrpc requests from body
	b, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
		return
	}

	req, requests, _ := ParseJsonRpcMessage(b)
	if req != nil {
		h.handleHttpSingle(req, w, r, next, startTime)
	} else {
		h.handleHttpBatch(requests, w, r, next, startTime)
	}
}

func (h *JsonRpcHandler) handleHttpSingle(request *JsonRpcMsg, w http.ResponseWriter, r *http.Request,
	next func(http.ResponseWriter, *http.Request), startTime time.Time) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, rule := range h.rules {
		hash := request.Hash()
		match := rule.Match(request)
		if match {
			switch rule.Action {
			case RuleActionAllow:
				if rule.Cache != nil {
					if h.cache.Has(hash) {
						h.writeSingleResponse(w, h.cache.Get(hash).Value().CloneWithID(request.ID))
						duration := time.Since(startTime)
						if h.responseTimeHist != nil {
							h.responseTimeHist.WithLabelValues(
								request.Method,
								request.MaybeGetPath(),
								cacheHit,
								RuleActionAllow).Observe(duration.Seconds())
						}
						h.log.WithFields(map[string]interface{}{
							"method":   request.Method,
							"path":     request.Params,
							"cache":    cacheHit,
							"duration": duration,
							"source":   GetSourceIP(r),
						}).Info("request allowed")
						return
					}
					h.getSingleUpstreamResponse(w, r, next, hash, rule.Cache)
					duration := time.Since(startTime)
					if h.responseTimeHist != nil {
						h.responseTimeHist.WithLabelValues(
							request.Method,
							request.MaybeGetPath(),
							cacheMiss,
							RuleActionAllow).Observe(duration.Seconds())
					}
					h.log.WithFields(map[string]interface{}{
						"method":   request.Method,
						"params":   request.Params,
						"cache":    cacheMiss,
						"duration": duration,
						"source":   GetSourceIP(r),
					}).Info("request allowed")
					return
				}
				next(w, r)
				duration := time.Since(startTime)
				if h.responseTimeHist != nil {
					h.responseTimeHist.WithLabelValues(
						request.Method,
						request.MaybeGetPath(),
						cacheMiss,
						RuleActionAllow).Observe(duration.Seconds())
				}
				h.log.WithFields(map[string]interface{}{
					"method":   request.Method,
					"params":   request.Params,
					"duration": duration,
					"source":   GetSourceIP(r),
				}).Info("request allowed")
				return

			case RuleActionDeny:
				h.writeSingleResponse(w, UnauthorizedResponse(request))
				duration := time.Since(startTime)
				if h.responseTimeHist != nil {
					h.responseTimeHist.WithLabelValues(
						request.Method,
						request.MaybeGetPath(),
						cacheMiss,
						RuleActionDeny).Observe(duration.Seconds())
				}
				h.log.WithFields(map[string]interface{}{
					"method":   request.Method,
					"params":   request.Params,
					"duration": duration,
					"source":   GetSourceIP(r),
				}).Info("request denied")
				return

			default:
				h.log.Errorf("unrecognized rule action %q", rule.Action)
			}
		}
	}
	if h.defaultAction == RuleActionAllow {
		next(w, r)
		duration := time.Since(startTime)
		if h.responseTimeHist != nil {
			h.responseTimeHist.WithLabelValues(
				request.Method,
				request.MaybeGetPath(),
				cacheMiss,
				RuleActionAllow).Observe(duration.Seconds())
		}
		h.log.WithFields(map[string]interface{}{
			"method":   request.Method,
			"params":   request.Params,
			"duration": duration,
			"source":   GetSourceIP(r),
		}).Info("request allowed")
	} else {
		h.writeSingleResponse(w, UnauthorizedResponse(request))
		duration := time.Since(startTime)
		if h.responseTimeHist != nil {
			h.responseTimeHist.WithLabelValues(
				request.Method,
				request.MaybeGetPath(),
				cacheMiss,
				RuleActionDeny).Observe(duration.Seconds())
		}
		h.log.WithFields(map[string]interface{}{
			"method":   request.Method,
			"params":   request.Params,
			"duration": duration,
			"source":   GetSourceIP(r),
		}).Info("request denied")
	}
}

func (h *JsonRpcHandler) getSingleUpstreamResponse(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request), hash uint64, cache *RuleCache) {
	ww := WrapResponseWriter(w)
	next(ww, r)

	b, err := ww.GetWrittenBytes()
	if err != nil {
		h.log.Errorf("error getting data from upstream response: %v", err)
		return
	}
	res, _, _ := ParseJsonRpcMessage(b)
	h.cache.Set(hash, res, cache.TTL)
}

func (h *JsonRpcHandler) writeSingleResponse(w http.ResponseWriter, res *JsonRpcMsg) {
	b, err := res.Marshal()
	if err != nil {
		h.log.Errorf("error marshalling response from cache: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

func (h *JsonRpcHandler) handleHttpBatch(requests JsonRpcMsgs, w http.ResponseWriter, r *http.Request,
	next func(http.ResponseWriter, *http.Request), startTime time.Time) {
	var responses JsonRpcResponses

	var cacheHits, cacheMisses, allowed, denied int

	h.mu.RLock()
	for _, req := range requests {
		if h.rules == nil || len(h.rules) == 0 {
			cacheMisses++
			if h.defaultAction == RuleActionAllow {
				_ = responses.AddPendingOrLoadFromCache(req, nil, nil, 0)
				allowed++
			} else {
				responses.Deny(req)
				denied++
			}
			continue
		}
		for _, rule := range h.rules {
			match := rule.Match(req)
			if match {
				switch rule.Action {
				case RuleActionAllow:
					allowed++
					h.log.WithFields(map[string]interface{}{
						"method": req.Method,
						"params": req.Params,
					}).Debug("request allowed")
					hit := responses.AddPendingOrLoadFromCache(req, h.cache, rule.Cache, req.Hash())
					if hit {
						cacheHits++
					} else {
						cacheMisses++
					}

				case RuleActionDeny:
					denied++
					h.log.WithFields(map[string]interface{}{
						"method": req.Method,
						"params": req.Params,
					}).Debug("request denied")
					responses.Deny(req)

				default:
					h.log.Errorf("unrecognized rule action %q", rule.Action)
				}
				break
			}
		}
	}
	h.mu.RUnlock()

	// send pending requests to upstream and grab the response
	pendingRequests := responses.GetPendingRequests()
	if len(pendingRequests) > 0 {
		h.log.Debug("getting from upstream")
		upstreamResponses, err := h.getResponsesFromUpstream(r, responses.GetPendingRequests(), next)
		if err != nil {
			h.log.Errorf("error getting responses from upstream: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		responses.Set(responses.GetPendingRequests(), upstreamResponses)
		responses.StoreInCache(h.cache)
	}

	b, err := responses.GetFinal().Marshal()
	if err != nil {
		h.log.Errorf("error marshalling response: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(b)
	duration := time.Since(startTime)
	if h.batchResTimeHist != nil {
		h.batchResTimeHist.WithLabelValues(
			strconv.Itoa(len(requests)),
			strconv.Itoa(allowed),
			strconv.Itoa(denied),
			strconv.Itoa(cacheHits),
			strconv.Itoa(cacheMisses),
		).Observe(duration.Seconds())
	}
	h.log.WithFields(map[string]interface{}{
		"requests":     len(requests),
		"allowed":      allowed,
		"denied":       denied,
		"cache_hits":   cacheHits,
		"cache_misses": cacheMisses,
		"duration":     duration,
		"source":       GetSourceIP(r),
	}).Info("processed batch of requests")
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
