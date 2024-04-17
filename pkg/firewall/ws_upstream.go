package firewall

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"github.com/NibiruChain/cosmos-firewall/pkg/util"
)

const (
	jsonRpcVersion     = "2.0"
	connectTimeout     = 10 * time.Second
	connectRetryPeriod = 5 * time.Second
	responseTimeout    = 10 * time.Second
)

var (
	ErrSubscriptionExists = errors.New("subscription already exists")
)

type UpstreamConnManager struct {
	client *JsonRpcWsClient
	url    url.URL
	dialer *websocket.Dialer
	log    *log.Entry
	IdGen  *util.UniqueID

	respMap map[int]chan *JsonRpcMsg
	respMux sync.Mutex

	onSubscriptionMessage func(msg *JsonRpcMsg)
	subByID               map[int]string
	subByQuery            map[string]int
	subMux                sync.Mutex
}

func NewUpstreamConnManager(url url.URL, idGen *util.UniqueID, onSubscriptionMessage func(msg *JsonRpcMsg)) *UpstreamConnManager {
	return &UpstreamConnManager{
		url: url,
		dialer: &websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: connectTimeout,
		},
		IdGen:                 idGen,
		subByQuery:            make(map[string]int),
		subByID:               make(map[int]string),
		respMap:               make(map[int]chan *JsonRpcMsg),
		onSubscriptionMessage: onSubscriptionMessage,
	}
}

func (u *UpstreamConnManager) Run(log *log.Entry) error {
	u.log = log
	for {
		if u.client == nil || u.client.IsClosed() {
			if err := u.connect(); err != nil {
				u.log.Errorf("error connecting: %v", err)
				time.Sleep(connectRetryPeriod)
				continue
			}
			if err := u.reSubmitSubscriptions(); err != nil {
				u.log.Errorf("error re-submitting subscriptions: %v", err)
				u.client.Close()
				continue
			}
		}
		msg, err := u.client.ReceiveMsg()
		if err != nil {
			if errors.Is(err, ErrClosed) {
				u.log.Errorf("websocket closed: %v", err)
				u.client.Close()
			} else {
				u.log.Errorf("error receiving message from upstream: %v", err)
			}
			continue
		}
		u.onUpstreamMessage(msg)
	}
}

func (u *UpstreamConnManager) connect() error {
	u.log.Debug("connecting to upstream websocket")
	conn, _, err := u.dialer.Dial(u.url.String(), nil)
	if err == nil {
		u.log.Info("upstream websocket connected")
		u.client = NewJsonRpcWsClient(conn)
	}
	return err
}

func (u *UpstreamConnManager) onUpstreamMessage(msg *JsonRpcMsg) {
	// Let's first check if it's a response to a request
	wc, ok := u.respMap[msg.ID.(int)]
	if ok {
		u.log.WithField("ID", msg.ID).Debug("got response for request")
		wc <- msg
		close(wc)
		return
	}

	// Otherwise let's check if it's a subscription notification
	query, ok := u.subByID[msg.ID.(int)]
	if ok {
		u.log.WithFields(map[string]interface{}{
			"ID":    msg.ID,
			"query": query,
		}).Debug("got message from subscription")
		u.onSubscriptionMessage(msg)
		return
	}

	u.log.Errorf("dropped message from upstream with ID: %v", msg.ID)
}

func (u *UpstreamConnManager) makeRequestWithID(id int, req *JsonRpcMsg) (*JsonRpcMsg, error) {
	request := req.CloneWithID(id)

	u.respMux.Lock()
	u.respMap[id] = make(chan *JsonRpcMsg)
	u.respMux.Unlock()

	u.log.WithFields(map[string]interface{}{
		"ID":     id,
		"method": request.Method,
	}).Debug("submitting request")
	if err := u.client.SendMsg(request); err != nil {
		return nil, err
	}

	select {
	case response := <-u.respMap[id]:
		u.respMux.Lock()
		delete(u.respMap, id)
		u.respMux.Unlock()

		response.ID = req.ID
		return response, nil

	case <-time.After(responseTimeout):
		u.respMux.Lock()
		delete(u.respMap, id)
		u.respMux.Unlock()

		return nil, fmt.Errorf("timeout waiting for response for request with ID %d", id)
	}
}

func (u *UpstreamConnManager) MakeRequest(req *JsonRpcMsg) (*JsonRpcMsg, error) {
	// Generate unique ID for request
	ID := u.IdGen.ID()
	defer u.IdGen.Release(ID)
	return u.makeRequestWithID(ID, req)
}

func (u *UpstreamConnManager) HasSubscription(query string) bool {
	_, ok := u.subByQuery[query]
	return ok
}

func (u *UpstreamConnManager) subscribeWithID(id int, query string) error {
	// Send subscribe request
	_, err := u.makeRequestWithID(id, &JsonRpcMsg{
		Version: jsonRpcVersion,
		ID:      id,
		Method:  methodSubscribe,
		Params:  map[string]string{"query": query},
	})
	if err != nil {
		u.IdGen.Release(id)
		return err
	}

	u.subMux.Lock()
	defer u.subMux.Unlock()
	u.subByQuery[query] = id
	u.subByID[id] = query

	u.log.WithFields(map[string]interface{}{
		"ID":    id,
		"query": query,
	}).Debug("registered subscription with upstream")
	return nil
}

func (u *UpstreamConnManager) Subscribe(query string) (int, error) {
	// Check if this is already subscribed
	if u.HasSubscription(query) {
		return -1, ErrSubscriptionExists
	}

	// Generate unique ID for subscription
	ID := u.IdGen.ID()
	return ID, u.subscribeWithID(ID, query)
}

func (u *UpstreamConnManager) UnsubscribeByID(id int) error {
	query, ok := u.subByID[id]
	if !ok {
		return fmt.Errorf("subscription with ID %d not found", id)
	}
	return u.Unsubscribe(query)
}

func (u *UpstreamConnManager) Unsubscribe(query string) error {
	_, err := u.MakeRequest(&JsonRpcMsg{
		Version: jsonRpcVersion,
		Method:  methodUnsubscribe,
		Params:  map[string]string{"query": query},
	})
	if err != nil {
		return err
	}

	u.subMux.Lock()
	defer u.subMux.Unlock()
	ID := u.subByQuery[query]
	delete(u.subByID, ID)
	delete(u.subByQuery, query)
	u.IdGen.Release(ID)

	u.log.WithFields(map[string]interface{}{
		"ID":    ID,
		"query": query,
	}).Debug("removed subscription with upstream")
	return nil
}

func (u *UpstreamConnManager) reSubmitSubscriptions() error {
	if len(u.subByQuery) > 0 {
		u.log.Info("re-submitting subscriptions")
		for query, id := range u.subByQuery {
			u.log.WithFields(map[string]interface{}{
				"ID":    id,
				"query": query,
			}).Debug("re-submitting subscription")
			if err := u.subscribeWithID(id, query); err != nil {
				return err
			}
		}
	}
	return nil
}

type UpstreamPool struct {
	conn    []*UpstreamConnManager
	connIdx uint32
	log     *log.Entry
	IdGen   *util.UniqueID

	subscriptionConn map[string]*UpstreamConnManager
	subscriptionID   map[string]int
	subMux           sync.Mutex

	onSubscriptionMessage func(*JsonRpcMsg)
}

func NewUpstreamPool(backend string, n int, onMessage func(*JsonRpcMsg)) *UpstreamPool {
	pool := &UpstreamPool{
		conn:                  make([]*UpstreamConnManager, n),
		subscriptionConn:      make(map[string]*UpstreamConnManager),
		subscriptionID:        make(map[string]int),
		onSubscriptionMessage: onMessage,
		IdGen:                 &util.UniqueID{},
	}

	backendUrl := url.URL{Scheme: "ws", Host: backend, Path: websocketPath}
	for i := 0; i < n; i++ {
		pool.conn[i] = NewUpstreamConnManager(backendUrl, pool.IdGen, pool.onSubscriptionMessage)
	}

	return pool
}

func (p *UpstreamPool) Start(log *log.Entry) error {
	p.log = log
	for i, conn := range p.conn {
		go func(id int, c *UpstreamConnManager) {
			if err := c.Run(p.log.WithField("upstream-id", id)); err != nil {
				p.log.Errorf("error on upstream connection: %v", err)
			}
		}(i, conn)
	}
	return nil
}

func (p *UpstreamPool) getConnection() (*UpstreamConnManager, error) {
	// TODO: this uses round-robin. Maybe get the connection with least subscriptions.
	// TODO: make sure we return a connection that is not closed
	n := atomic.AddUint32(&p.connIdx, 1)
	return p.conn[(int(n)-1)%len(p.conn)], nil
}

func (p *UpstreamPool) MakeRequest(msg *JsonRpcMsg) (*JsonRpcMsg, error) {
	conn, err := p.getConnection()
	if err != nil {
		return nil, err
	}
	return conn.MakeRequest(msg)
}

func (p *UpstreamPool) Subscribe(query string) (int, error) {
	p.subMux.Lock()
	defer p.subMux.Unlock()

	// Check if this query is subscribed already
	if id, ok := p.subscriptionID[query]; ok {
		return id, nil
	}

	conn, err := p.getConnection()
	if err != nil {
		return -1, err
	}

	id, err := conn.Subscribe(query)
	if err != nil {
		return -1, err
	}

	p.subscriptionConn[query] = conn
	p.subscriptionID[query] = id
	return id, nil
}

func (p *UpstreamPool) Unsubscribe(query string) error {
	p.subMux.Lock()
	defer p.subMux.Unlock()

	conn, ok := p.subscriptionConn[query]
	if !ok {
		return nil
	}
	if err := conn.Unsubscribe(query); err != nil {
		return err
	}

	delete(p.subscriptionConn, query)
	delete(p.subscriptionID, query)
	return nil
}
