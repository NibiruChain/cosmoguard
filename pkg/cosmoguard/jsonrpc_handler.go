package cosmoguard

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

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"

	"github.com/NibiruChain/cosmoguard/pkg/cache"
)

type JsonRpcHandler struct {
	cache            cache.Cache[uint64, *JsonRpcMsg]
	defaultAction    RuleAction
	wsProxy          *JsonRpcWebSocketProxy
	wsPath           string
	rules            []*JsonRpcRule
	rulesMutex       sync.RWMutex // Mutex to block readers when rules are being updated
	hash             *maphash.Hash
	log              *log.Entry
	responseTimeHist *prometheus.HistogramVec
	batchResTimeHist *prometheus.HistogramVec
}

func NewJsonRpcHandler(name string, opts ...Option[JsonRpcHandlerOptions]) (*JsonRpcHandler, error) {
	cfg := DefaultJsonRpcHandlerOptions()
	for _, opt := range opts {
		opt(cfg)
	}
	handler := &JsonRpcHandler{
		hash:   &maphash.Hash{},
		wsPath: cfg.WebsocketPath,
	}

	// Setup cache
	var cacheOptions []cache.Option
	if cfg.CacheConfig != nil {
		cacheOptions = append(cacheOptions, cache.DefaultTTL(cfg.CacheConfig.TTL))
	}

	var err error
	if cfg.CacheConfig != nil && cfg.CacheConfig.Redis != nil {
		handler.cache, err = cache.NewRedisCache[uint64, *JsonRpcMsg](*cfg.CacheConfig.Redis, cacheOptions...)
		if err != nil {
			return nil, err
		}
	} else {
		handler.cache, err = cache.NewMemoryCache[uint64, *JsonRpcMsg](cacheOptions...)
		if err != nil {
			return nil, err
		}
	}

	if cfg.WebsocketEnabled {
		handler.wsProxy = NewJsonRpcWebSocketProxy(
			name,
			cfg.WebsocketBackend,
			cfg.WebsocketPath,
			cfg.WebsocketConnections,
			cfg.UpstreamConstructor,
			handler.cache,
			cfg.MetricsEnabled,
		)
	}

	if cfg.MetricsEnabled {
		handler.responseTimeHist = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: name,
			Name:      "request_duration_seconds",
			Help:      "Histogram of response time for handler in seconds",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "path", "cache", "cosmoguard"})
		handler.batchResTimeHist = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: fmt.Sprintf("%s_batch", name),
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

	if h.wsProxy != nil {
		go func() {
			if err := h.wsProxy.Run(h.log); err != nil {
				h.log.Errorf("error on websocket proxy: %v", err)
			}
		}()
	}
	return nil
}

func (h *JsonRpcHandler) SetRules(rules []*JsonRpcRule, defaultAction RuleAction) {
	h.rulesMutex.Lock()
	defer h.rulesMutex.Unlock()

	h.rules = rules
	h.defaultAction = defaultAction
	if h.wsProxy != nil {
		h.wsProxy.SetRules(rules, defaultAction)
	}
}

func (h *JsonRpcHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request)) {
	start := time.Now()

	if r.Method == http.MethodPost && r.URL.Path == "/" {
		h.log.Debug("serving http jsonrpc request")
		h.handleHttp(w, r, next, start)
		return
	}

	if h.wsProxy != nil && r.URL.Path == h.wsPath && r.Method == http.MethodGet {
		h.log.Debug("handling jsonrpc websocket connection")
		h.wsProxy.HandleConnection(w, r)
		return
	}

	h.log.WithFields(map[string]interface{}{
		"method": r.Method,
		"path":   r.URL.Path,
	}).Errorf("unexpected request")
	WriteError(w, http.StatusBadRequest, "unexpected request")
}

func (h *JsonRpcHandler) handleHttp(w http.ResponseWriter, r *http.Request,
	next func(http.ResponseWriter, *http.Request), startTime time.Time) {
	var err error
	r.Body = ReusableReader(r.Body)

	// Get jsonrpc requests from body
	b, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad request")
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
	h.rulesMutex.RLock()
	defer h.rulesMutex.RUnlock()

	for _, rule := range h.rules {
		hash := request.Hash()
		match := rule.Match(request)
		if match {
			switch rule.Action {
			case RuleActionAllow:
				if rule.Cache != nil {
					cached, err := h.cache.Has(r.Context(), hash)
					if err != nil {
						h.log.Errorf("error getting cached value: %v", err)
					}
					if cached {
						res, err := h.cache.Get(r.Context(), hash)
						if err == nil {
							h.writeSingleResponse(w, res.CloneWithID(request.ID))

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
						h.log.Errorf("error retrieving from cache: %v", err)
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

	if err = h.cache.Set(r.Context(), hash, res, cache.TTL); err != nil {
		h.log.Errorf("error setting cache value: %v", err)
	}
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
	responses := JsonRpcResponses{}

	var cacheHits, cacheMisses, allowed, denied int

	h.rulesMutex.RLock()
RequestsLoop:
	for _, req := range requests {
		if h.rules == nil || len(h.rules) == 0 {
			cacheMisses++
			if h.defaultAction == RuleActionAllow {
				responses.AddPending(req)
				allowed++
			} else {
				responses.Deny(req)
				denied++
			}
			continue RequestsLoop
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

					cached, err := h.cache.Has(r.Context(), req.Hash())
					if err != nil {
						h.log.Errorf("error getting cached value: %v", err)
						responses.AddPendingWithCacheConfig(req, req.Hash(), rule.Cache)
						cacheMisses++
						continue RequestsLoop
					}

					if cached {
						res, err := h.cache.Get(r.Context(), req.Hash())
						if err == nil {
							cacheHits++
							responses.AddResponse(req, res)
							continue RequestsLoop
						}
						h.log.Errorf("error loading response from cache: %v", err)
					}

					cacheMisses++
					responses.AddPendingWithCacheConfig(req, req.Hash(), rule.Cache)
					continue RequestsLoop

				case RuleActionDeny:
					denied++
					h.log.WithFields(map[string]interface{}{
						"method": req.Method,
						"params": req.Params,
					}).Debug("request denied")
					responses.Deny(req)
					continue RequestsLoop

				default:
					h.log.Errorf("unrecognized rule action %q", rule.Action)
				}
				break
			}
		}
		if h.defaultAction == RuleActionAllow {
			responses.AddPending(req)
			allowed++
		}
	}
	h.rulesMutex.RUnlock()

	// send pending requests to upstream and grab the response
	pendingRequests := responses.GetPendingRequests()
	if len(pendingRequests) > 0 {
		h.log.Debug("getting from upstream")
		upstreamResponses, err := h.getResponsesFromUpstream(r, pendingRequests, next)
		if err != nil {
			h.log.Errorf("error getting responses from upstream: %v", err)
			WriteError(w, http.StatusInternalServerError, "error getting responses from upstream")
			return
		}
		if len(upstreamResponses) == len(pendingRequests) {
			responses.Set(pendingRequests, upstreamResponses)
			if err = responses.StoreInCache(h.cache); err != nil {
				h.log.Errorf("error caching responses: %v", err)
			}
		}
	}

	b, err := responses.GetFinal().Marshal()
	if err != nil {
		h.log.Errorf("error marshalling response: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	WriteData(w, http.StatusOK, b)

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
	single, responses, _ := ParseJsonRpcMessage(b)
	if len(responses) == 0 && single != nil {
		responses = JsonRpcMsgs{single}
	}
	return responses, nil
}
