package firewall

import (
	"maps"
	"sync"
)

type SubscriptionManager struct {
	subscriptionMux sync.RWMutex
	queryToID       map[string]int
	idToQuery       map[int]string

	clientsMux          sync.RWMutex
	clientSubscriptions map[int]map[*JsonRpcWsClient]interface{}
}

func NewSubscriptionManager() *SubscriptionManager {
	return &SubscriptionManager{
		queryToID:           make(map[string]int),
		idToQuery:           make(map[int]string),
		clientSubscriptions: make(map[int]map[*JsonRpcWsClient]interface{}),
	}
}

func (s *SubscriptionManager) AddSubscription(query string, id int) {
	s.subscriptionMux.Lock()
	defer s.subscriptionMux.Unlock()
	s.queryToID[query] = id
	s.idToQuery[id] = query

	s.clientsMux.Lock()
	defer s.clientsMux.Unlock()
	s.clientSubscriptions[id] = make(map[*JsonRpcWsClient]interface{})
}

func (s *SubscriptionManager) RemoveSubscription(id int) {
	s.subscriptionMux.Lock()
	defer s.subscriptionMux.Unlock()
	delete(s.queryToID, s.idToQuery[id])
	delete(s.idToQuery, id)

	s.clientsMux.Lock()
	defer s.clientsMux.Unlock()
	delete(s.clientSubscriptions, id)
}

func (s *SubscriptionManager) SubscriptionEmpty(id int) bool {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()
	return len(s.clientSubscriptions[id]) == 0
}

func (s *SubscriptionManager) SubscribeClient(id int, client *JsonRpcWsClient, clientSubID interface{}) {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	s.clientsMux.Lock()
	defer s.clientsMux.Unlock()
	s.clientSubscriptions[id][client] = clientSubID
}

func (s *SubscriptionManager) UnsubscribeClient(id int, client *JsonRpcWsClient) {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	s.clientsMux.Lock()
	defer s.clientsMux.Unlock()
	delete(s.clientSubscriptions[id], client)
}

func (s *SubscriptionManager) ClientSubscribed(id int, client *JsonRpcWsClient) bool {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()
	_, ok := s.clientSubscriptions[id][client]
	return ok
}

func (s *SubscriptionManager) GetSubscriptions(client *JsonRpcWsClient) []int {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()

	list := make([]int, 0)
	for id, m := range s.clientSubscriptions {
		if _, ok := m[client]; ok {
			list = append(list, id)
		}
	}
	return list
}

func (s *SubscriptionManager) GetSubscriptionID(query string) (int, bool) {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	id, ok := s.queryToID[query]
	return id, ok
}

func (s *SubscriptionManager) GetSubscriptionQuery(id int) (string, bool) {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	q, ok := s.idToQuery[id]
	return q, ok
}

func (s *SubscriptionManager) GetSubscriptionClients(id int) map[*JsonRpcWsClient]interface{} {
	s.subscriptionMux.RLock()
	defer s.subscriptionMux.RUnlock()
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()
	return maps.Clone(s.clientSubscriptions[id])
}
