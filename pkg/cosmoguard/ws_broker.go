package cosmoguard

import (
	"fmt"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/NibiruChain/cosmoguard/pkg/util"
)

type Broker struct {
	IdGen          *util.UniqueID
	pool           *UpstreamPool
	log            *log.Entry
	sm             *SubscriptionManager
	upstreamSubMux sync.Mutex
}

func NewBroker(backend, path string, n int, upstreamConstructor UpstreamConnManagerConstructor) *Broker {
	b := Broker{
		IdGen: &util.UniqueID{},
		sm:    NewSubscriptionManager(),
	}
	b.pool = NewUpstreamPool(backend, path, n, b.onSubscriptionMessage, upstreamConstructor)
	return &b
}

func (b *Broker) Start(log *log.Entry) error {
	b.log = log
	return b.pool.Start(log)
}

func (b *Broker) HandleRequest(msg *JsonRpcMsg) (*JsonRpcMsg, error) {
	res, err := b.pool.MakeRequest(msg)
	if err != nil {
		return nil, err
	}
	return res.CloneWithID(msg.ID), nil
}

func (b *Broker) HandleSubscription(client *JsonRpcWsClient, msg *JsonRpcMsg) (*JsonRpcMsg, error) {
	b.log.WithField("client", client).Debug("handling subscription")
	client.SetOnDisconnectCallback(b.onClientDisconnect)

	switch msg.Method {
	case methodSubscribeCosmos:
		_, err := b.addSubscription(client, msg)
		if err != nil {
			return ErrorResponse(msg, -100, err.Error(), nil), nil
		}
		return EmptyResult(msg), nil

	case methodUnsubscribeCosmos:
		if err := b.removeSubscription(client, msg); err != nil {
			return ErrorResponse(msg, -100, err.Error(), nil), nil
		}
		return EmptyResult(msg), nil

	case methodUnsubscribeAllCosmos:
		if err := b.removeAllSubscriptions(client); err != nil {
			return nil, err
		}
		return EmptyResult(msg), nil

	case methodSubscribeEth:
		id, err := b.addSubscription(client, msg)
		if err != nil {
			return ErrorResponse(msg, -100, err.Error(), nil), nil
		}
		return WithResult(msg, id), nil

	case methodUnsubscribeEth:
		if err := b.removeSubscription(client, msg); err != nil {
			return ErrorResponse(msg, -100, err.Error(), nil), nil
		}
		return WithResult(msg, true), nil

	default:
		// This should never happen
		return nil, fmt.Errorf("unsupported method on subscriptions")
	}
}

func (b *Broker) addSubscription(client *JsonRpcWsClient, msg *JsonRpcMsg) (string, error) {
	param, err := getSubscriptionParam(msg)
	if err != nil {
		return "", err
	}

	b.upstreamSubMux.Lock()
	defer b.upstreamSubMux.Unlock()

	id, exists := b.sm.GetSubscriptionID(param)
	if !exists {
		b.log.WithField("client", client).Debug("upstream subscription does not exist")

		// Subscribe in upstream
		id, err = b.pool.Subscribe(param)
		if err != nil {
			return "", err
		}
		b.sm.AddSubscription(param, id)

		b.log.WithFields(map[string]interface{}{
			"id":    id,
			"param": param,
		}).Info("subscribed upstream")
	}

	if msg.Method == methodSubscribeEth {
		// Eth does not send the ID on subscription notifications. Lets do the same
		b.sm.SubscribeClient(id, client, nil)
	} else {
		b.sm.SubscribeClient(id, client, msg.ID)
	}

	b.log.WithFields(map[string]interface{}{
		"id":     id,
		"client": client,
	}).Debug("subscribed client")

	return id, nil
}

func (b *Broker) removeSubscription(client *JsonRpcWsClient, msg *JsonRpcMsg) error {
	b.log.WithField("client", client).Debug("unsubscribing client")
	subParam, err := getSubscriptionParam(msg)
	if err != nil {
		return err
	}

	var subID string
	if isEthSubscriptionID(subParam) {
		subID = subParam
	} else {
		id, exists := b.sm.GetSubscriptionID(subParam)
		if !exists {
			return fmt.Errorf("subscription does not exist")
		}
		subID = id
	}

	if !b.sm.ClientSubscribed(subID, client) {
		return fmt.Errorf("subscription does not exist")
	}

	b.sm.UnsubscribeClient(subID, client)
	b.log.WithFields(map[string]interface{}{
		"id":     subID,
		"client": client,
	}).Debug("unsubscribed client")

	b.upstreamSubMux.Lock()
	defer b.upstreamSubMux.Unlock()

	if b.sm.SubscriptionEmpty(subID) {
		if err = b.pool.Unsubscribe(subID); err != nil {
			return err
		}
		param, _ := b.sm.GetSubscriptionParam(subID)
		b.sm.RemoveSubscription(subID)

		b.log.WithFields(map[string]interface{}{
			"ID":    subID,
			"param": param,
		}).Warn("unsubscribed upstream")
	}

	return nil
}

func (b *Broker) removeAllSubscriptions(client *JsonRpcWsClient) error {
	b.log.WithField("client", client).Debug("unsubscribing client from all subscriptions")

	b.upstreamSubMux.Lock()
	defer b.upstreamSubMux.Unlock()

	for _, subscriptionID := range b.sm.GetSubscriptions(client) {
		b.log.WithField("ID", subscriptionID).Debug("unsubscribing client")
		b.sm.UnsubscribeClient(subscriptionID, client)

		if b.sm.SubscriptionEmpty(subscriptionID) {
			b.log.WithField("ID", subscriptionID).Debug("unsubscribing upstream")
			if err := b.pool.Unsubscribe(subscriptionID); err != nil {
				return err
			}
			b.sm.RemoveSubscription(subscriptionID)

			param, _ := b.sm.GetSubscriptionParam(subscriptionID)
			b.log.WithFields(map[string]interface{}{
				"ID":    subscriptionID,
				"param": param,
			}).Warn("unsubscribed upstream")

		}
	}
	return nil
}

func (b *Broker) onSubscriptionMessage(msg *JsonRpcMsg) {
	msgID := msg.ID.(string)

	clients := b.sm.GetSubscriptionClients(msgID)
	if len(clients) == 0 {
		b.log.WithField("ID", msg.ID).Warn("no subscribers for message")
		return
	}

	b.log.WithFields(map[string]interface{}{
		"ID":      msgID,
		"clients": len(clients),
	}).Info("broadcasting message to subscribers")

	for client, id := range clients {
		go func(client *JsonRpcWsClient, id interface{}) {
			if err := client.SendMsg(msg.CloneWithID(id)); err != nil {
				b.log.Errorf("error sending message to client: %v", err)
			}
		}(client, id)
	}
}

func (b *Broker) onClientDisconnect(client *JsonRpcWsClient) {
	b.log.Warn("removing all subscriptions for client")
	if err := b.removeAllSubscriptions(client); err != nil {
		b.log.Errorf("error removing all subscriptions for client: %v", err)
	}
}
