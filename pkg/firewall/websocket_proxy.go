package firewall

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
)

const (
	websocketPath = "/websocket"
)

type JsonRpcWebSocketProxy struct {
	pool          *UpstreamConnectionPool
	cache         *ttlcache.Cache[uint64, *JsonRpcMsg]
	wsBackend     string
	upgrader      *websocket.Upgrader
	rules         []*JsonRpcRule
	defaultAction RuleAction
	mu            sync.RWMutex
	log           *log.Entry
}

type UpstreamConnection struct {
	conn *websocket.Conn
}

func NewJsonRpcWebSocketProxy(backend string, connections int, cache *ttlcache.Cache[uint64, *JsonRpcMsg]) *JsonRpcWebSocketProxy {
	return &JsonRpcWebSocketProxy{
		pool:      NewUpstreamConnectionPool(backend, connections),
		wsBackend: backend,
		upgrader:  &websocket.Upgrader{},
		cache:     cache,
	}
}

func (h *JsonRpcWebSocketProxy) Start(log *log.Entry) error {
	h.log = log.WithField("type", "websocket")
	return h.pool.Start(h.log)
}

func (h *JsonRpcWebSocketProxy) SetRules(rules []*JsonRpcRule, defaultAction RuleAction) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rules = rules
	h.defaultAction = defaultAction
}

func (h *JsonRpcWebSocketProxy) HandleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Errorf("error upgrading connection to websocket: %v", err)
		return
	}
	defer conn.Close()

	writeCh := make(chan []byte)
	defer close(writeCh)
	defer h.pool.DisconnectChannel(writeCh)

	go func(c *websocket.Conn) {
		for {
			msg, ok := <-writeCh
			if !ok {
				return
			}
			if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
				h.log.Errorf("error writing message: %v", err)
			}
		}
	}(conn)

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.log.Errorf("error reading message: %v", err)
			}
			break
		}
		if string(message) == "" {
			continue
		}
		request, _, err := ParseJsonRpcMessage(message)
		if err != nil {
			h.log.Errorf("error parsing jsonrpc from message: %v", err)
			continue
		}

		if request == nil {
			h.log.Errorf("bad request: %s", string(message))
			continue
		}

		if err := h.handleRequest(writeCh, request); err != nil {
			h.log.Errorf("error handling request: %v", err)
			continue
		}
	}
}

func (h *JsonRpcWebSocketProxy) handleRequest(writeCh chan []byte, request *JsonRpcMsg) error {
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
						h.log.WithFields(map[string]interface{}{
							"method": request.Method,
							"params": request.Params,
							"cache":  "hit",
						}).Info("request allowed")
						return h.replyTo(writeCh, h.cache.Get(hash).Value())
					}
					h.log.WithFields(map[string]interface{}{
						"method": request.Method,
						"params": request.Params,
						"cache":  "miss",
					}).Info("request allowed")
				} else {
					h.log.WithFields(map[string]interface{}{
						"method": request.Method,
						"params": request.Params,
					}).Info("request allowed")
				}
				response, err := h.pool.SubmitRequest(request, writeCh)
				if err != nil {
					return err
				}
				if rule.Cache != nil {
					h.cache.Set(hash, response, rule.Cache.TTL)
				}
				return h.replyTo(writeCh, response)

			case RuleActionDeny:
				h.log.WithFields(map[string]interface{}{
					"method": request.Method,
					"params": request.Params,
				}).Info("request denied")
				return h.replyTo(writeCh, UnauthorizedResponse(request))

			default:
				h.log.Errorf("unrecognized rule action %q", rule.Action)
			}
		}
	}
	if h.defaultAction == RuleActionAllow {
		response, err := h.pool.SubmitRequest(request, writeCh)
		if err != nil {
			return err
		}
		return h.replyTo(writeCh, response)
	} else {
		return h.replyTo(writeCh, UnauthorizedResponse(request))
	}
}

func (h *JsonRpcWebSocketProxy) replyTo(writeCh chan []byte, response *JsonRpcMsg) error {
	b, err := response.Marshal()
	if err != nil {
		return fmt.Errorf("error marshalling response: %v", err)
	}
	writeCh <- b
	return nil
}
