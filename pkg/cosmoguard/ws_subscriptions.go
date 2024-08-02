package cosmoguard

import (
	"maps"
	"sync"
)

type SubscriptionManager struct {
	subscriptionMux sync.RWMutex
	paramToID       map[string]string
	idToParam       map[string]string

	clientsMux          sync.RWMutex
	clientSubscriptions map[string]map[*JsonRpcWsClient]interface{}
}

func NewSubscriptionManager() *SubscriptionManager {
	return &SubscriptionManager{
		paramToID:           make(map[string]string),
		idToParam:           make(map[string]string),
		clientSubscriptions: make(map[string]map[*JsonRpcWsClient]interface{}),
	}
}

func (s *SubscriptionManager) AddSubscription(param string, id string) {
	s.subscriptionMux.Lock()
	defer s.subscriptionMux.Unlock()
	s.paramToID[param] = id
	s.idToParam[id] = param

	s.clientsMux.Lock()
	defer s.clientsMux.Unlock()
	s.clientSubscriptions[id] = make(map[*JsonRpcWsClient]interface{})
}

func (s *SubscriptionManager) RemoveSubscription(id string) {
	s.subscriptionMux.Lock()
	defer s.subscriptionMux.Unlock()
	delete(s.paramToID, s.idToParam[id])
	delete(s.idToParam, id)

	s.clientsMux.Lock()
	defer s.clientsMux.Unlock()
	delete(s.clientSubscriptions, id)
}

func (s *SubscriptionManager) SubscriptionEmpty(id string) bool {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()
	return len(s.clientSubscriptions[id]) == 0
}

func (s *SubscriptionManager) SubscribeClient(id string, client *JsonRpcWsClient, clientSubID interface{}) {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	s.clientsMux.Lock()
	defer s.clientsMux.Unlock()
	s.clientSubscriptions[id][client] = clientSubID
}

func (s *SubscriptionManager) UnsubscribeClient(id string, client *JsonRpcWsClient) {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	s.clientsMux.Lock()
	defer s.clientsMux.Unlock()
	delete(s.clientSubscriptions[id], client)
}

func (s *SubscriptionManager) ClientSubscribed(id string, client *JsonRpcWsClient) bool {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()
	_, ok := s.clientSubscriptions[id][client]
	return ok
}

func (s *SubscriptionManager) GetSubscriptions(client *JsonRpcWsClient) []string {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()

	list := make([]string, 0)
	for id, m := range s.clientSubscriptions {
		if _, ok := m[client]; ok {
			list = append(list, id)
		}
	}
	return list
}

func (s *SubscriptionManager) GetSubscriptionID(param string) (string, bool) {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	id, ok := s.paramToID[param]
	return id, ok
}

func (s *SubscriptionManager) GetSubscriptionParam(id string) (string, bool) {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	q, ok := s.idToParam[id]
	return q, ok
}

func (s *SubscriptionManager) GetSubscriptionClients(id string) map[*JsonRpcWsClient]interface{} {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()
	return maps.Clone(s.clientSubscriptions[id])
}
