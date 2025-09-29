package cosmoguard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"

	"github.com/NibiruChain/cosmoguard/pkg/cache"
)

const (
	defaultWebsocketPath = "/websocket"
)

type JsonRpcWebSocketProxy struct {
	broker           *Broker
	cache            cache.Cache[uint64, *JsonRpcMsg]
	wsBackend        string
	upgrader         *websocket.Upgrader
	rules            []*JsonRpcRule
	defaultAction    RuleAction
	rulesMutex       sync.RWMutex
	log              *log.Entry
	responseTimeHist *prometheus.HistogramVec
}

func NewJsonRpcWebSocketProxy(name, backend, path string, connections int, upstreamConstructor UpstreamConnManagerConstructor,
	cache cache.Cache[uint64, *JsonRpcMsg], metricsEnabled bool) *JsonRpcWebSocketProxy {
	proxy := &JsonRpcWebSocketProxy{
		broker:    NewBroker(backend, path, connections, upstreamConstructor),
		wsBackend: backend,
		upgrader:  &websocket.Upgrader{},
		cache:     cache,
	}

	proxy.upgrader.CheckOrigin = func(r *http.Request) bool {
		return true
	}

	if metricsEnabled {
		proxy.responseTimeHist = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: fmt.Sprintf("websocket_%s", name),
			Name:      "request_duration_seconds",
			Help:      "Histogram of response time for handler in seconds",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "path", "cache", "cosmoguard"})
	}

	return proxy
}

func (p *JsonRpcWebSocketProxy) Run(log *log.Entry) error {
	p.log = log.WithField("type", "websocket")
	if p.responseTimeHist != nil {
		prometheus.MustRegister(p.responseTimeHist)
	}
	return p.broker.Start(p.log)
}

func (p *JsonRpcWebSocketProxy) SetRules(rules []*JsonRpcRule, defaultAction RuleAction) {
	p.rulesMutex.Lock()
	defer p.rulesMutex.Unlock()

	p.rules = rules
	p.defaultAction = defaultAction
}

func (p *JsonRpcWebSocketProxy) HandleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		p.log.Errorf("error upgrading connection to websocket: %v", err)
		return
	}

	client := NewJsonRpcWsClient(conn)
	defer client.Close()

	for {
		req, err := client.ReceiveMsg()
		if err != nil {
			if errors.Is(err, ErrClosed) {
				p.log.Warnf("client disconnected")
				client.Close()
				break
			}
			p.log.Errorf("error reading message: %v", err)
			continue
		}

		if err := p.handleRequest(client, req, GetSourceIP(r)); err != nil {
			p.log.Errorf("error handling request: %v", err)
			continue
		}
	}
}

func (p *JsonRpcWebSocketProxy) handleRequest(client *JsonRpcWsClient, request *JsonRpcMsg, source string) error {
	p.rulesMutex.RLock()
	defer p.rulesMutex.RUnlock()

	startTime := time.Now()

	for _, rule := range p.rules {
		hash := request.Hash()
		match := rule.Match(request)
		if match {
			switch rule.Action {
			case RuleActionAllow:
				if rule.Cache != nil {
					cached, err := p.cache.Has(context.Background(), hash)
					if err != nil {
						p.log.Errorf("error getting cached value: %v", err)
					}
					if cached {
						res, err := p.cache.Get(context.Background(), hash)
						if err != nil {
							return err
						}
						if err = client.SendMsg(res.CloneWithID(request.ID)); err != nil {
							return err
						}

						duration := time.Since(startTime)
						p.log.WithFields(map[string]interface{}{
							"id":       request.ID,
							"method":   request.Method,
							"params":   request.Params,
							"cache":    cacheHit,
							"duration": duration,
							"source":   source,
						}).Info("request allowed")

						if p.responseTimeHist != nil {
							p.responseTimeHist.WithLabelValues(
								request.Method,
								request.MaybeGetPath(),
								cacheHit,
								RuleActionAllow,
							).Observe(duration.Seconds())
						}
						return nil
					}
				}

				var res *JsonRpcMsg
				var err error
				if hasSubscriptionMethod(request) {
					res, err = p.broker.HandleSubscription(client, request)
					if err != nil {
						return err
					}
				} else {
					res, err = p.broker.HandleRequest(request)
					if err != nil {
						return err
					}
				}

				if err = client.SendMsg(res); err != nil {
					return err
				}

				duration := time.Since(startTime)
				p.log.WithFields(map[string]interface{}{
					"id":       request.ID,
					"method":   request.Method,
					"params":   request.Params,
					"cache":    cacheMiss,
					"duration": duration,
					"source":   source,
				}).Info("request allowed")

				if p.responseTimeHist != nil {
					p.responseTimeHist.WithLabelValues(
						request.Method,
						request.MaybeGetPath(),
						cacheMiss,
						RuleActionAllow,
					).Observe(duration.Seconds())
				}

				if rule.Cache == nil {
					return nil
				}

				if res.Error != nil && !rule.Cache.CacheError {
					return nil
				}
				if res.Result == nil && !rule.Cache.CacheEmptyResult {
					return nil
				}
				if err = p.cache.Set(context.Background(), hash, res, rule.Cache.TTL); err != nil {
					return fmt.Errorf("error storing in cache: %v", err)
				}
				return nil

			case RuleActionDeny:
				if err := client.SendMsg(UnauthorizedResponse(request)); err != nil {
					return err
				}

				duration := time.Since(startTime)
				p.log.WithFields(map[string]interface{}{
					"id":       request.ID,
					"method":   request.Method,
					"params":   request.Params,
					"duration": duration,
					"source":   source,
				}).Info("request denied")

				if p.responseTimeHist != nil {
					p.responseTimeHist.WithLabelValues(
						request.Method,
						request.MaybeGetPath(),
						cacheMiss,
						RuleActionDeny,
					).Observe(duration.Seconds())
				}
				return nil

			default:
				p.log.Errorf("unrecognized rule action %q", rule.Action)
			}
		}
	}

	if p.defaultAction == RuleActionAllow {
		var res *JsonRpcMsg
		var err error
		if hasSubscriptionMethod(request) {
			res, err = p.broker.HandleSubscription(client, request)
			if err != nil {
				return err
			}
		} else {
			res, err = p.broker.HandleRequest(request)
			if err != nil {
				return err
			}
		}

		if err = client.SendMsg(res); err != nil {
			return err
		}

	} else {
		if err := client.SendMsg(UnauthorizedResponse(request)); err != nil {
			return err
		}
	}

	duration := time.Since(startTime)
	p.log.WithFields(map[string]interface{}{
		"id":       request.ID,
		"method":   request.Method,
		"params":   request.Params,
		"duration": duration,
		"source":   source,
	}).Infof("request %s", p.defaultAction)

	if p.responseTimeHist != nil {
		p.responseTimeHist.WithLabelValues(
			request.Method,
			request.MaybeGetPath(),
			cacheMiss,
			string(p.defaultAction),
		).Observe(duration.Seconds())
	}
	return nil
}
