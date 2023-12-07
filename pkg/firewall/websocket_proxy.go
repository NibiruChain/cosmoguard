package firewall

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jellydator/ttlcache/v3"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

const (
	websocketPath = "/websocket"
)

type JsonRpcWebSocketProxy struct {
	pool             *UpstreamConnectionPool
	cache            *ttlcache.Cache[uint64, *JsonRpcMsg]
	wsBackend        string
	upgrader         *websocket.Upgrader
	rules            []*JsonRpcRule
	defaultAction    RuleAction
	mu               sync.RWMutex
	log              *log.Entry
	responseTimeHist *prometheus.HistogramVec
}

type UpstreamConnection struct {
	conn *websocket.Conn
}

func NewJsonRpcWebSocketProxy(backend string, connections int, cache *ttlcache.Cache[uint64, *JsonRpcMsg], metricsEnabled bool) *JsonRpcWebSocketProxy {
	proxy := &JsonRpcWebSocketProxy{
		pool:      NewUpstreamConnectionPool(backend, connections),
		wsBackend: backend,
		upgrader:  &websocket.Upgrader{},
		cache:     cache,
	}

	if metricsEnabled {
		proxy.responseTimeHist = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "websocket_jsonrpc",
			Name:      "request_duration_seconds",
			Help:      "Histogram of response time for handler in seconds",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "path", "cache", "firewall"})
	}

	return proxy
}

func (p *JsonRpcWebSocketProxy) Start(log *log.Entry) error {
	p.log = log.WithField("type", "websocket")
	if p.responseTimeHist != nil {
		prometheus.MustRegister(p.responseTimeHist)
	}
	return p.pool.Start(p.log)
}

func (p *JsonRpcWebSocketProxy) SetRules(rules []*JsonRpcRule, defaultAction RuleAction) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules = rules
	p.defaultAction = defaultAction
}

func (p *JsonRpcWebSocketProxy) HandleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		p.log.Errorf("error upgrading connection to websocket: %v", err)
		return
	}
	defer conn.Close()

	writeCh := make(chan []byte)
	defer close(writeCh)
	defer p.pool.DisconnectChannel(writeCh)

	go func(c *websocket.Conn) {
		for {
			msg, ok := <-writeCh
			if !ok {
				return
			}
			if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
				p.log.Errorf("error writing message: %v", err)
			}
		}
	}(conn)

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				p.log.Errorf("error reading message: %v", err)
			}
			break
		}
		startTime := time.Now()
		if string(message) == "" {
			continue
		}
		request, _, err := ParseJsonRpcMessage(message)
		if err != nil {
			p.log.Errorf("error parsing jsonrpc from message: %v", err)
			continue
		}

		if request == nil {
			p.log.Errorf("bad request: %s", string(message))
			continue
		}

		if err := p.handleRequest(writeCh, request, startTime); err != nil {
			p.log.Errorf("error handling request: %v", err)
			continue
		}
	}
}

func (p *JsonRpcWebSocketProxy) handleRequest(writeCh chan []byte, request *JsonRpcMsg, startTime time.Time) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, rule := range p.rules {
		hash := request.Hash()
		match := rule.Match(request)
		if match {
			switch rule.Action {
			case RuleActionAllow:
				if rule.Cache != nil {
					if p.cache.Has(hash) {
						if err := p.replyTo(writeCh, p.cache.Get(hash).Value()); err != nil {
							return err
						}
						duration := time.Since(startTime)
						p.log.WithFields(map[string]interface{}{
							"method":   request.Method,
							"params":   request.Params,
							"cache":    cacheHit,
							"duration": duration,
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
				response, err := p.pool.SubmitRequest(request, writeCh)
				if err != nil {
					return err
				}
				if err = p.replyTo(writeCh, response); err != nil {
					return err
				}
				duration := time.Since(startTime)
				p.log.WithFields(map[string]interface{}{
					"method":   request.Method,
					"params":   request.Params,
					"cache":    cacheMiss,
					"duration": duration,
				}).Info("request allowed")
				if p.responseTimeHist != nil {
					p.responseTimeHist.WithLabelValues(
						request.Method,
						request.MaybeGetPath(),
						cacheMiss,
						RuleActionAllow,
					).Observe(duration.Seconds())
				}
				if rule.Cache != nil {
					p.cache.Set(hash, response, rule.Cache.TTL)
				}
				return nil

			case RuleActionDeny:
				if err := p.replyTo(writeCh, UnauthorizedResponse(request)); err != nil {
					return err
				}
				duration := time.Since(startTime)
				p.log.WithFields(map[string]interface{}{
					"method":   request.Method,
					"params":   request.Params,
					"duration": duration,
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
		response, err := p.pool.SubmitRequest(request, writeCh)
		if err != nil {
			return err
		}
		if err = p.replyTo(writeCh, response); err != nil {
			return err
		}
	} else {
		if err := p.replyTo(writeCh, UnauthorizedResponse(request)); err != nil {
			return err
		}
	}
	duration := time.Since(startTime)
	p.log.WithFields(map[string]interface{}{
		"method":   request.Method,
		"params":   request.Params,
		"duration": duration,
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

func (p *JsonRpcWebSocketProxy) replyTo(writeCh chan []byte, response *JsonRpcMsg) error {
	b, err := response.Marshal()
	if err != nil {
		return fmt.Errorf("error marshalling response: %v", err)
	}
	writeCh <- b
	return nil
}
