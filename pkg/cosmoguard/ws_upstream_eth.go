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

type UpstreamConnManagerEth struct {
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

func EthUpstreamConnManager(url url.URL, idGen *util.UniqueID, onSubscriptionMessage func(msg *JsonRpcMsg)) UpstreamConnManager {
	return &UpstreamConnManagerEth{
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

func (u *UpstreamConnManagerEth) Run(log *log.Entry) error {
	u.log = log
	for {
		if u.client == nil || u.client.IsClosed() {
			if err := u.connect(); err != nil {
				u.log.Errorf("error connecting: %v", err)
				time.Sleep(connectRetryPeriod)
				continue
			}
			u.resetAll()
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

func (u *UpstreamConnManagerEth) connect() error {
	u.log.Debug("connecting to upstream websocket")
	conn, _, err := u.dialer.Dial(u.url.String(), nil)
	if err == nil {
		u.log.Info("upstream websocket connected")
		u.client = NewJsonRpcWsClient(conn)
	}
	return err
}

func (u *UpstreamConnManagerEth) onUpstreamMessage(msg *JsonRpcMsg) {
	// This is a subscription notification
	if msg.ID == nil {
		if msg.Params == nil {
			u.log.Errorf("dropped message from upstream with no ID and no params")
			return
		}
		params := msg.Params.(map[string]interface{})
		sub, ok := params["subscription"]
		if !ok {
			u.log.Errorf("dropped message from upstream with no ID")
			return
		}

		subscriptionID := sub.(string)
		msg.ID = subscriptionID
		query, ok := u.subByID[subscriptionID]
		if ok {
			u.log.WithFields(map[string]interface{}{
				"ID":    subscriptionID,
				"query": query,
			}).Debug("got message from subscription")
			u.onSubscriptionMessage(msg)
			return
		}
	} else {
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

		wc, ok := u.respMap[msgID]
		if ok {
			u.log.WithField("ID", msgID).Debug("got response for request")
			wc <- msg
			close(wc)
			return
		}
	}

	u.log.Errorf("dropped message from upstream with ID: %v", msg.ID)
}

func (u *UpstreamConnManagerEth) makeRequestWithID(id string, req *JsonRpcMsg) (*JsonRpcMsg, error) {
	request := req.CloneWithID(id)

	// Check if client is connected before attempting to send
	if u.client == nil || u.client.IsClosed() {
		return nil, ErrClosed
	}

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

func (u *UpstreamConnManagerEth) MakeRequest(req *JsonRpcMsg) (*JsonRpcMsg, error) {
	// Generate unique ID for request
	ID := u.IdGen.ID()
	defer u.IdGen.Release(ID)
	return u.makeRequestWithID(ID, req)
}

func (u *UpstreamConnManagerEth) HasSubscription(subID string) bool {
	_, ok := u.subByParam[subID]
	return ok
}

func (u *UpstreamConnManagerEth) Subscribe(query string) (string, error) {
	// Check if this is already subscribed
	if u.HasSubscription(query) {
		return "", ErrSubscriptionExists
	}

	// Generate unique ID for subscription
	ID := u.IdGen.ID()
	return u.subscribeWithID(ID, query)
}

func (u *UpstreamConnManagerEth) subscribeWithID(id string, param string) (string, error) {
	defer u.IdGen.Release(id)

	// Send subscribe request
	msg := &JsonRpcMsg{
		Version: jsonRpcVersion,
		ID:      id,
		Method:  methodSubscribeEth,
		Params:  []interface{}{param},
	}

	resp, err := u.makeRequestWithID(id, msg)
	if err != nil {
		return "", err
	}

	subID := resp.Result.(string)

	u.subMux.Lock()
	defer u.subMux.Unlock()

	u.subByParam[param] = subID
	u.subByID[subID] = param

	u.log.WithFields(map[string]interface{}{
		"ID":    subID,
		"param": param,
	}).Debug("registered subscription with upstream")
	return subID, nil
}

func (u *UpstreamConnManagerEth) Unsubscribe(id string) error {
	msg := &JsonRpcMsg{
		Version: jsonRpcVersion,
		Method:  methodUnsubscribeEth,
		Params:  []string{id},
	}

	_, err := u.MakeRequest(msg)
	if err != nil {
		return err
	}

	u.subMux.Lock()
	defer u.subMux.Unlock()

	param := u.subByID[id]
	delete(u.subByID, id)
	delete(u.subByParam, param)

	u.log.WithFields(map[string]interface{}{
		"ID":    id,
		"param": param,
	}).Debug("removed subscription with upstream")
	return nil
}

func (u *UpstreamConnManagerEth) resetAll() {
	u.subMux.Lock()
	defer u.subMux.Unlock()

	u.subByParam = make(map[string]string)
	u.subByID = make(map[string]string)
	u.respMap = make(map[string]chan *JsonRpcMsg)
}

func (u *UpstreamConnManagerEth) reSubmitSubscriptions() error {
	if len(u.subByParam) > 0 {
		u.log.Info("re-submitting subscriptions")
		for param := range u.subByParam {
			u.log.WithFields(map[string]interface{}{
				"param": param,
			}).Debug("re-submitting subscription")
			if _, err := u.Subscribe(param); err != nil {
				return err
			}
		}
	}
	return nil
}
