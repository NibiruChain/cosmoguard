package firewall

import (
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
	jsonRpcVersion       = "2.0"
	connectRetryPeriod   = 5 * time.Second
	connectTimeout       = 10 * time.Second
	responseTimeout      = 5 * time.Second
	methodSubscribe      = "subscribe"
	methodUnsubscribe    = "unsubscribe"
	methodUnsubscribeAll = "unsubscribe_all"
)

type upstreamConnection struct {
	conn       *websocket.Conn
	url        url.URL
	readMutex  sync.Mutex
	writeMutex sync.Mutex
	log        *log.Entry
	dialer     *websocket.Dialer
}

func newUpstreamConnection(url url.URL) *upstreamConnection {
	return &upstreamConnection{
		url: url,
		dialer: &websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: connectTimeout,
		},
	}
}

func (u *upstreamConnection) Receive() (messageType int, p []byte, err error) {
	u.readMutex.Lock()
	defer u.readMutex.Unlock()
	return u.conn.ReadMessage()
}

func (u *upstreamConnection) Send(b []byte) error {
	u.writeMutex.Lock()
	defer u.writeMutex.Unlock()
	return u.conn.WriteMessage(websocket.TextMessage, b)
}

func (u *upstreamConnection) connect() error {
	u.log.Debug("connecting to upstream websocket")
	var err error
	u.conn, _, err = u.dialer.Dial(u.url.String(), nil)
	if err == nil {
		u.log.Debug("upstream websocket connected")
	}
	return err
}

func (u *upstreamConnection) Run(log *log.Entry, onNewMessage func([]byte), onConnect func(connection *upstreamConnection)) error {
	u.log = log
	for {
		if u.conn == nil {
			if err := u.connect(); err != nil {
				u.log.Errorf("error connecting: %v", err)
				time.Sleep(connectRetryPeriod)
				continue
			}
			onConnect(u)
		}
		_, message, err := u.Receive()
		if err != nil {
			u.log.Errorf("websocket closed: %v", err)
			u.conn = nil
			continue
		}
		onNewMessage(message)
	}
}

type UpstreamConnectionPool struct {
	upstream     []*upstreamConnection
	connIdx      uint32
	IdGen        *util.UniqueID
	respChannels *sync.Map

	log *log.Entry

	subscriptionUpstreamConnection *sync.Map //map[int]*upstreamConnection
	subscriptionChannels           *sync.Map //map[int]map[chan []byte]interface{}
	subscriptions                  *sync.Map //map[string]int
	clientSubscriptions            *sync.Map //map[chan []byte][]int
}

func NewUpstreamConnectionPool(backend string, n int) *UpstreamConnectionPool {
	pool := &UpstreamConnectionPool{
		upstream:     make([]*upstreamConnection, n),
		IdGen:        &util.UniqueID{},
		respChannels: &sync.Map{},

		subscriptionUpstreamConnection: &sync.Map{},
		subscriptionChannels:           &sync.Map{},
		subscriptions:                  &sync.Map{},
		clientSubscriptions:            &sync.Map{},
	}

	backendUrl := url.URL{Scheme: "ws", Host: backend, Path: websocketPath}
	for i := 0; i < n; i++ {
		pool.upstream[i] = newUpstreamConnection(backendUrl)
	}

	return pool
}

func (u *UpstreamConnectionPool) Start(log *log.Entry) error {
	u.log = log
	for i, conn := range u.upstream {
		go func(id int, c *upstreamConnection) {
			if err := c.Run(u.log.WithField("id", id), u.onConnectionMessage, u.onUpstreamConnected); err != nil {
				u.log.Errorf("error on upstream connection: %v", err)
			}
		}(i, conn)
	}
	return nil
}

func (u *UpstreamConnectionPool) getConnection() *upstreamConnection {
	// Use round-robin. TODO: maybe get the connection with least subscriptions
	n := atomic.AddUint32(&u.connIdx, 1)
	return u.upstream[(int(n)-1)%len(u.upstream)]
}

func (u *UpstreamConnectionPool) SubmitRequest(msg *JsonRpcMsg, writeCh chan []byte) (*JsonRpcMsg, error) {
	if hasSubscriptionMethod(msg) {
		return u.handleSubscription(msg, writeCh)
	}
	// Create channel to receive response and store it in temporary map
	resp := make(chan *JsonRpcMsg)
	defer close(resp)
	ID := u.IdGen.ID()
	defer u.IdGen.Release(ID)
	u.respChannels.Store(ID, resp)

	// Clone request and change ID to something we know won't have duplicates
	b, err := msg.CloneWithID(ID).Marshal()
	if err != nil {
		return nil, err
	}
	upstream := u.getConnection()
	if err := upstream.Send(b); err != nil {
		return nil, err
	}

	select {
	case response := <-resp:
		response.ID = msg.ID
		return response, nil

	case <-time.After(responseTimeout):
		u.respChannels.Delete(ID)
		return nil, fmt.Errorf("timeout waiting for response for request with ID %d", ID)
	}
}

func (u *UpstreamConnectionPool) handleSubscription(request *JsonRpcMsg, writeCh chan []byte) (*JsonRpcMsg, error) {
	switch request.Method {
	case methodSubscribe:
		return u.addSubscription(request, writeCh)

	case methodUnsubscribe:
		return u.removeSubscription(request, writeCh)

	case methodUnsubscribeAll:
		return u.removeAllSubscriptions(request, writeCh)

	default:
		// This should never happen
		return nil, fmt.Errorf("unsupported method on subscriptions")
	}
}

func (u *UpstreamConnectionPool) addSubscription(request *JsonRpcMsg, writeCh chan []byte) (*JsonRpcMsg, error) {
	query, err := getSubscriptionQuery(request)
	if err != nil {
		return ErrorResponse(request, -100, err.Error(), nil), nil
	}

	var subscriptionID int
	v, ok := u.subscriptions.Load(query)
	if !ok {
		// Create ID and channel for subscription
		subscriptionID = u.IdGen.ID()
		ch := make(chan *JsonRpcMsg)

		// Store ID, channel and a new upstream connection
		u.respChannels.Store(subscriptionID, ch)
		u.subscriptions.Store(query, subscriptionID)
		conn := u.getConnection()
		u.subscriptionUpstreamConnection.Store(subscriptionID, conn)

		// Send subscription upstream
		b, err := request.CloneWithID(subscriptionID).Marshal()
		if err != nil {
			return nil, err
		}
		if err := conn.Send(b); err != nil {
			return nil, err
		}
		go u.startSubscriptionRoutine(subscriptionID, ch)

		select {
		case <-ch:
		case <-time.After(responseTimeout):
			u.respChannels.Delete(subscriptionID)
			u.subscriptions.Delete(query)
			return nil, fmt.Errorf("timeout waiting for response for request with ID %d", subscriptionID)
		}
	} else {
		subscriptionID = v.(int)
	}

	if v, ok = u.clientSubscriptions.Load(writeCh); ok {
		u.clientSubscriptions.Store(writeCh, append(v.([]int), subscriptionID))
	} else {
		u.clientSubscriptions.Store(writeCh, []int{subscriptionID})
	}

	var channelMap *sync.Map
	if v, ok = u.subscriptionChannels.Load(subscriptionID); ok {
		channelMap = v.(*sync.Map)
	} else {
		channelMap = &sync.Map{}
	}
	channelMap.Store(writeCh, request.ID)
	u.subscriptionChannels.Store(subscriptionID, channelMap)

	return EmptyResult(request), nil
}

func (u *UpstreamConnectionPool) removeSubscription(request *JsonRpcMsg, writeCh chan []byte) (*JsonRpcMsg, error) {
	query, err := getSubscriptionQuery(request)
	if err != nil {
		return nil, err
	}

	var subscriptionID int
	v, ok := u.subscriptions.Load(query)
	if !ok {
		return ErrorResponse(request, -32603, "Internal error", "subscription not found"), nil
	}
	subscriptionID = v.(int)

	if _, ok = u.clientSubscriptions.Load(writeCh); !ok {
		return ErrorResponse(request, -32603, "Internal error", "subscription not found"), nil
	}

	u.clientSubscriptions.Delete(writeCh)

	v, ok = u.subscriptionChannels.Load(subscriptionID)
	if !ok {
		return ErrorResponse(request, -32603, "Internal error", "cannot load subscriptions"), nil
	}
	v.(*sync.Map).Delete(writeCh)

	length := 0
	v.(*sync.Map).Range(func(_, _ interface{}) bool {
		length++
		return true
	})

	if length == 0 {
		u.subscriptions.Range(func(_, id any) bool {
			if id.(int) == subscriptionID {
				u.destroyUpstreamSubscription(subscriptionID, query)
			}
			return true
		})
		u.respChannels.Delete(subscriptionID)
		u.subscriptionChannels.Delete(subscriptionID)
		u.subscriptionUpstreamConnection.Delete(subscriptionID)
	}

	return EmptyResult(request), nil
}

func (u *UpstreamConnectionPool) onUpstreamConnected(upstream *upstreamConnection) {
	u.subscriptionUpstreamConnection.Range(func(key, value any) bool {
		subscriptionID := key.(int)
		conn := value.(*upstreamConnection)
		if conn == upstream {
			u.log.WithField("subscription", subscriptionID).Info("restoring subscription")
			u.subscriptions.Range(func(key, value any) bool {
				query := key.(string)
				if subscriptionID == value.(int) {
					msg := &JsonRpcMsg{
						Version: jsonRpcVersion,
						ID:      subscriptionID,
						Method:  methodSubscribe,
						Params:  []string{query},
					}
					b, _ := msg.Marshal()
					if err := upstream.Send(b); err != nil {
						log.WithField("subscription", subscriptionID).Errorf("error restoring subscription: %v", err)
					}
				}
				return true
			})
		}
		return true
	})
}

func (u *UpstreamConnectionPool) removeAllSubscriptions(request *JsonRpcMsg, writeCh chan []byte) (*JsonRpcMsg, error) {
	u.DisconnectChannel(writeCh)
	return EmptyResult(request), nil
}

func (u *UpstreamConnectionPool) startSubscriptionRoutine(id int, subscriptionChannel chan *JsonRpcMsg) {
	u.log.WithField("subscriptionID", id).Info("new upstream subscription started")
	for {
		msg, ok := <-subscriptionChannel
		if !ok {
			u.log.WithField("subscriptionID", id).Warn("upstream subscription canceled")
			break
		}
		v, ok := u.subscriptionChannels.Load(id)
		if !ok {
			u.log.WithField("subscriptionID", id).Warn("no channels connected to subscription")
		}
		u.log.WithField("subscriptionID", id).Info("broadcasting message to subscribers")
		v.(*sync.Map).Range(func(key, value any) bool {
			ch := key.(chan []byte)
			originID := value.(interface{})
			b, _ := msg.CloneWithID(originID).Marshal()
			ch <- b
			return true
		})
	}
}

func (u *UpstreamConnectionPool) DisconnectChannel(writeCh chan []byte) {
	v, ok := u.clientSubscriptions.LoadAndDelete(writeCh)
	if !ok {
		return
	}

	for _, subID := range v.([]int) {
		v, ok = u.subscriptionChannels.Load(subID)
		if !ok {
			continue
		}
		v.(*sync.Map).Delete(writeCh)

		length := 0
		v.(*sync.Map).Range(func(_, _ any) bool {
			length++
			return true
		})

		if length == 0 {
			u.subscriptions.Range(func(key, value any) bool {
				query := key.(string)
				id := value.(int)
				if id == subID {
					u.destroyUpstreamSubscription(subID, query)
				}
				return true
			})
			u.respChannels.Delete(subID)
			u.subscriptionChannels.Delete(subID)
			u.subscriptionUpstreamConnection.Delete(subID)
		}
	}
}

func (u *UpstreamConnectionPool) destroyUpstreamSubscription(subID int, query string) {
	unsubscribeMsg := &JsonRpcMsg{
		Version: jsonRpcVersion,
		ID:      subID,
		Method:  methodUnsubscribe,
		Params:  []string{query},
	}

	// Lets close subscription channel to cancel its goroutine
	v, _ := u.respChannels.LoadAndDelete(subID)
	ch, _ := v.(chan *JsonRpcMsg)
	if ch != nil {
		close(ch)
	}

	tmpCh := make(chan *JsonRpcMsg)
	u.respChannels.Store(subID, tmpCh)
	b, _ := unsubscribeMsg.Marshal()

	v, ok := u.subscriptionUpstreamConnection.Load(subID)
	if !ok {
		log.Errorf("error loading upstream connection")
		return
	}
	if err := v.(*upstreamConnection).Send(b); err != nil {
		log.Errorf("error sending unsubscribe message upstream: %v", err)
	}
	<-tmpCh
	close(tmpCh)
	u.subscriptions.Delete(query)
}

func (u *UpstreamConnectionPool) onConnectionMessage(b []byte) {
	msg, _, err := ParseJsonRpcMessage(b)
	if err != nil {
		log.Errorf("error parsing message: %v", err)
	}
	u.onNewMsg(msg)
}

func (u *UpstreamConnectionPool) onNewMsg(msg *JsonRpcMsg) {
	v, ok := u.respChannels.Load(msg.ID)
	if !ok {
		log.Errorf("error loading channel for response")
		return
	}

	respCh, ok := v.(chan *JsonRpcMsg)
	if !ok {
		log.Errorf("error casting response to jsonrpc channel")
		return
	}
	respCh <- msg

}

func getSubscriptionQuery(request *JsonRpcMsg) (string, error) {
	if params, ok := request.Params.(map[string]interface{}); ok {
		if query, ok := params["query"].(string); ok {
			return query, nil
		}
	}

	params, ok := request.Params.([]interface{})
	if !ok {
		return "", fmt.Errorf("bad params for subscribe")
	}
	query, ok := params[0].(string)
	if !ok {
		return "", fmt.Errorf("bad query format (should be string)")
	}
	return query, nil
}

func hasSubscriptionMethod(request *JsonRpcMsg) bool {
	if request.Method == methodSubscribe ||
		request.Method == methodUnsubscribe ||
		request.Method == methodUnsubscribeAll {
		return true
	}
	return false
}
