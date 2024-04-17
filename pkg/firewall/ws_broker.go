package firewall

import (
	"fmt"

	log "github.com/sirupsen/logrus"

	"github.com/NibiruChain/cosmos-firewall/pkg/util"
)

type Broker struct {
	IdGen *util.UniqueID
	pool  *UpstreamPool
	log   *log.Entry
	sm    *SubscriptionManager
}

func NewBroker(backend string, n int) *Broker {
	b := Broker{
		IdGen: &util.UniqueID{},
		sm:    NewSubscriptionManager(),
	}
	b.pool = NewUpstreamPool(backend, n, b.onSubscriptionMessage)
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
	case methodSubscribe:
		return b.addSubscription(client, msg)

	case methodUnsubscribe:
		return b.removeSubscription(client, msg)

	case methodUnsubscribeAll:
		return b.removeAllSubscriptions(client, msg)

	default:
		// This should never happen
		return nil, fmt.Errorf("unsupported method on subscriptions")
	}
}

func (b *Broker) addSubscription(client *JsonRpcWsClient, msg *JsonRpcMsg) (*JsonRpcMsg, error) {
	query, err := getSubscriptionQuery(msg)
	if err != nil {
		return ErrorResponse(msg, -100, err.Error(), nil), nil
	}

	id, exists := b.sm.GetSubscriptionID(query)
	if !exists {
		b.log.WithField("client", client).Debug("upstream subscription does not exist")

		// Subscribe in upstream
		id, err = b.pool.Subscribe(query)
		if err != nil {
			return ErrorResponse(msg, -100, err.Error(), nil), nil
		}
		b.sm.AddSubscription(query, id)

		b.log.WithFields(map[string]interface{}{
			"id":    id,
			"query": query,
		}).Info("subscribed upstream")
	}

	b.sm.SubscribeClient(id, client, msg.ID)
	b.log.WithFields(map[string]interface{}{
		"id":     id,
		"client": client,
	}).Debug("subscribed client")

	return EmptyResult(msg), nil
}

func (b *Broker) removeSubscription(client *JsonRpcWsClient, msg *JsonRpcMsg) (*JsonRpcMsg, error) {
	b.log.WithField("client", client).Debug("unsubscribing client")
	query, err := getSubscriptionQuery(msg)
	if err != nil {
		return ErrorResponse(msg, -100, err.Error(), nil), nil
	}

	id, exists := b.sm.GetSubscriptionID(query)
	if !exists {
		return ErrorResponse(msg, -100, "subscription not found", nil), nil
	}

	if !b.sm.ClientSubscribed(id, client) {
		return ErrorResponse(msg, -100, "subscription not found", nil), nil
	}

	b.sm.UnsubscribeClient(id, client)
	b.log.WithFields(map[string]interface{}{
		"id":     id,
		"client": client,
	}).Debug("unsubscribed client")

	if b.sm.SubscriptionEmpty(id) {
		if err = b.pool.Unsubscribe(query); err != nil {
			return nil, err
		}
		b.sm.RemoveSubscription(id)

		b.log.WithFields(map[string]interface{}{
			"ID":    id,
			"query": query,
		}).Warn("unsubscribed upstream")
	}

	return EmptyResult(msg), err
}

func (b *Broker) removeAllSubscriptions(client *JsonRpcWsClient, msg *JsonRpcMsg) (*JsonRpcMsg, error) {
	b.log.WithField("client", client).Debug("unsubscribing client from all subscriptions")

	for _, subscriptionID := range b.sm.GetSubscriptions(client) {
		b.log.WithField("ID", subscriptionID).Debug("unsubscribing client")
		b.sm.UnsubscribeClient(subscriptionID, client)

		if b.sm.SubscriptionEmpty(subscriptionID) {
			b.log.WithField("ID", subscriptionID).Debug("unsubscribing upstream")
			query, exists := b.sm.GetSubscriptionQuery(subscriptionID)
			if exists {
				if err := b.pool.Unsubscribe(query); err != nil {
					return nil, err
				}
				b.sm.RemoveSubscription(subscriptionID)

				b.log.WithFields(map[string]interface{}{
					"ID":    subscriptionID,
					"query": query,
				}).Warn("unsubscribed upstream")
			}

		}
	}

	if msg != nil {
		return EmptyResult(msg), nil
	}
	return nil, nil
}

func (b *Broker) onSubscriptionMessage(msg *JsonRpcMsg) {
	msgID := msg.ID.(int)

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
	if _, err := b.removeAllSubscriptions(client, nil); err != nil {
		b.log.Errorf("error removing all subscriptions for client: %v", err)
	}
}
