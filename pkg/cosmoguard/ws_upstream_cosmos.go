package cosmoguard

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"github.com/NibiruChain/cosmoguard/pkg/util"
)

type UpstreamConnManagerCosmos struct {
	client *JsonRpcWsClient
	url    url.URL
	dialer *websocket.Dialer
	log    *log.Entry
	IdGen  *util.UniqueID

	respMap map[string]chan *JsonRpcMsg
	respMux sync.Mutex

	onSubscriptionMessage func(msg *JsonRpcMsg)
	subByID               map[string]string
	subByParam            map[string]string
	subMux                sync.Mutex
}

func CosmosUpstreamConnManager(url url.URL, idGen *util.UniqueID, onSubscriptionMessage func(msg *JsonRpcMsg)) UpstreamConnManager {
	return &UpstreamConnManagerCosmos{
		url: url,
		dialer: &websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: connectTimeout,
		},
		IdGen:                 idGen,
		subByParam:            make(map[string]string),
		subByID:               make(map[string]string),
		respMap:               make(map[string]chan *JsonRpcMsg),
		onSubscriptionMessage: onSubscriptionMessage,
	}
}

func (u *UpstreamConnManagerCosmos) Run(log *log.Entry) error {
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

func (u *UpstreamConnManagerCosmos) connect() error {
	u.log.Debug("connecting to upstream websocket")
	conn, _, err := u.dialer.Dial(u.url.String(), nil)
	if err == nil {
		u.log.Info("upstream websocket connected")
		u.client = NewJsonRpcWsClient(conn)
	}
	return err
}

func (u *UpstreamConnManagerCosmos) onUpstreamMessage(msg *JsonRpcMsg) {
	if msg.ID == nil {
		u.log.Errorf("dropped message from upstream with no ID")
		return
	}

	var msgID string

	switch v := msg.ID.(type) {
	case string:
		msgID = v
	case int:
		msgID = strconv.Itoa(v)
	default:
		u.log.WithField("ID", msg.ID).Error("wrong ID type")
		return
	}

	// Let's first check if it's a response to a request
	wc, ok := u.respMap[msgID]
	if ok {
		u.log.WithField("ID", msgID).Debug("got response for request")
		wc <- msg
		close(wc)
		return
	}

	// Otherwise let's check if it's a cosmos subscription notification
	param, ok := u.subByID[msgID]
	if ok {
		u.log.WithFields(map[string]interface{}{
			"ID":    msgID,
			"param": param,
		}).Debug("got message from subscription")
		u.onSubscriptionMessage(msg)
		return
	}

	u.log.Errorf("dropped message from upstream with ID: %v", msg.ID)
}

func (u *UpstreamConnManagerCosmos) makeRequestWithID(id string, req *JsonRpcMsg) (*JsonRpcMsg, error) {
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

		return nil, fmt.Errorf("timeout waiting for response for request with ID %s", id)
	}
}

func (u *UpstreamConnManagerCosmos) MakeRequest(req *JsonRpcMsg) (*JsonRpcMsg, error) {
	// Generate unique ID for request
	ID := u.IdGen.ID()
	defer u.IdGen.Release(ID)
	return u.makeRequestWithID(ID, req)
}

func (u *UpstreamConnManagerCosmos) HasSubscription(param string) bool {
	_, ok := u.subByParam[param]
	return ok
}

func (u *UpstreamConnManagerCosmos) Subscribe(param string) (string, error) {
	// Check if this is already subscribed
	if u.HasSubscription(param) {
		return "", ErrSubscriptionExists
	}

	// Generate unique ID for subscription
	ID := u.IdGen.ID()
	return ID, u.subscribeWithID(ID, param)
}

func (u *UpstreamConnManagerCosmos) subscribeWithID(id string, param string) error {
	// Send subscribe request
	msg := &JsonRpcMsg{
		Version: jsonRpcVersion,
		ID:      id,
		Method:  methodSubscribeCosmos,
		Params:  []interface{}{param},
	}

	_, err := u.makeRequestWithID(id, msg)
	if err != nil {
		u.IdGen.Release(id)
		return err
	}

	u.subMux.Lock()
	defer u.subMux.Unlock()

	u.subByParam[param] = id
	u.subByID[id] = param

	u.log.WithFields(map[string]interface{}{
		"ID":    id,
		"param": param,
	}).Debug("registered subscription with upstream")
	return nil
}

func (u *UpstreamConnManagerCosmos) Unsubscribe(id string) error {
	param := u.subByID[id]

	msg := &JsonRpcMsg{
		Version: jsonRpcVersion,
		Method:  methodUnsubscribeCosmos,
		Params:  []interface{}{param},
	}

	_, err := u.MakeRequest(msg)
	if err != nil {
		return err
	}

	u.subMux.Lock()
	defer u.subMux.Unlock()
	delete(u.subByID, id)
	delete(u.subByParam, param)
	u.IdGen.Release(id)

	u.log.WithFields(map[string]interface{}{
		"ID":    id,
		"param": param,
	}).Debug("removed subscription with upstream")
	return nil
}

func (u *UpstreamConnManagerCosmos) reSubmitSubscriptions() error {
	if len(u.subByParam) > 0 {
		u.log.Info("re-submitting subscriptions")
		for param, id := range u.subByParam {
			u.log.WithFields(map[string]interface{}{
				"ID":    id,
				"param": param,
			}).Debug("re-submitting subscription")
			if err := u.subscribeWithID(id, param); err != nil {
				return err
			}
		}
	}
	return nil
}
