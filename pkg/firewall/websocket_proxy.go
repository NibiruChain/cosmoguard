package firewall

import (
	"fmt"
	"net/http"
	"net/url"
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
	upstream      *websocket.Conn
	rules         []*JsonRpcRule
	defaultAction RuleAction
	mu            sync.RWMutex
}

type UpstreamConnection struct {
	conn *websocket.Conn
}

func NewJsonRpcWebSocketProxy(backend string, connections int, cache *ttlcache.Cache[uint64, *JsonRpcMsg]) (*JsonRpcWebSocketProxy, error) {
	pool, err := NewUpstreamConnectionPool(backend, connections)
	if err != nil {
		return nil, err
	}

	return &JsonRpcWebSocketProxy{
		pool:      pool,
		wsBackend: backend,
		upgrader:  &websocket.Upgrader{},
		cache:     cache,
	}, nil
}

func (h *JsonRpcWebSocketProxy) connectBackend() error {
	u := url.URL{Scheme: "ws", Host: h.wsBackend, Path: websocketPath}
	var err error
	h.upstream, _, err = websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to connect to upstream websocket: %v", err)
	}
	return nil
}

func (h *JsonRpcWebSocketProxy) Start() error {
	if err := h.pool.Start(); err != nil {
		return err
	}

	if err := h.connectBackend(); err != nil {
		return err
	}

	for {
		_, message, err := h.upstream.ReadMessage()
		if err != nil {
			log.Errorf("error reading message: %v", err)
			continue
		}
		msg, _, err := ParseJsonRpcMessage(message)
		if err != nil {
			log.Errorf("error parsing jsonrpc from message: %v", err)
			continue
		}
		h.onUpstreamMessage(msg)
	}
}

func (h *JsonRpcWebSocketProxy) SetRules(rules []*JsonRpcRule, defaultAction RuleAction) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rules = rules
	h.defaultAction = defaultAction
}

func (h *JsonRpcWebSocketProxy) onUpstreamMessage(msg *JsonRpcMsg) {
	log.Info("got subscribe event:", msg)
}

func (h *JsonRpcWebSocketProxy) HandleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Errorf("error upgrading connection to websocket: %v", err)
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
				log.Errorf("error writing message: %v", err)
			}
		}
	}(conn)

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Errorf("error reading message: %v", err)
			}
			break
		}
		if string(message) == "" {
			continue
		}
		request, _, err := ParseJsonRpcMessage(message)
		if err != nil {
			log.Errorf("error parsing jsonrpc from message: %v", err)
			continue
		}

		if request == nil {
			log.Errorf("bad request: %s", string(message))
			continue
		}

		if err := h.handleRequest(writeCh, request); err != nil {
			log.Errorf("error handling request: %v", err)
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
				log.Info("request allowed")
				if rule.Cache != nil {
					if h.cache.Has(hash) {
						log.Info("cache hit")
						return h.replyTo(writeCh, h.cache.Get(hash).Value())
					}
					log.Info("cache miss")
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
				log.Info("request denied")
				return h.replyTo(writeCh, UnauthorizedResponse(request))

			default:
				log.Errorf("unrecognized rule action %q", rule.Action)
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
