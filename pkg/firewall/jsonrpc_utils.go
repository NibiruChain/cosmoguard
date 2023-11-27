package firewall

import (
	"bytes"
	"net/http"

	"github.com/goccy/go-json"
	"github.com/jellydator/ttlcache/v3"
)

type JsonRpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type JsonRpcMsg struct {
	Version string        `json:"jsonrpc"`
	ID      interface{}   `json:"id,omitempty"`
	Method  string        `json:"method,omitempty"`
	Params  interface{}   `json:"params,omitempty"`
	Result  interface{}   `json:"result,omitempty"`
	Error   *JsonRpcError `json:"error,omitempty"`
}

func (j *JsonRpcMsg) Marshall() ([]byte, error) {
	return json.Marshal(j)
}

type JsonRpcMsgs []*JsonRpcMsg

func (j JsonRpcMsgs) Marshall() ([]byte, error) {
	return json.Marshal(j)
}

func ParseJsonRpcMessage(b []byte) (*JsonRpcMsg, JsonRpcMsgs, error) {
	if bytes.HasPrefix(b, []byte{'['}) && bytes.HasSuffix(b, []byte{']'}) {
		var msg JsonRpcMsgs
		err := json.Unmarshal(b, &msg)
		return nil, msg, err
	}
	var msg JsonRpcMsg
	err := json.Unmarshal(b, &msg)
	return &msg, nil, err
}

func UnauthorizedResponse(req *JsonRpcMsg) *JsonRpcMsg {
	return &JsonRpcMsg{
		Version: "2.0",
		Error: &JsonRpcError{
			Code:    http.StatusUnauthorized,
			Message: "unauthorized access",
		},
		ID: req.ID,
	}
}

type JsonRpcResponse struct {
	Request  *JsonRpcMsg
	Response *JsonRpcMsg
	Cache    *RuleCache
	CacheKey uint64
}

type JsonRpcResponses []*JsonRpcResponse

func (l *JsonRpcResponses) GetPendingRequests() JsonRpcMsgs {
	requests := make(JsonRpcMsgs, 0)
	for _, r := range *l {
		if r.Response == nil {
			requests = append(requests, r.Request)
		}
	}
	return requests
}

func (l *JsonRpcResponses) GetFinal() JsonRpcMsgs {
	responses := make(JsonRpcMsgs, 0)
	for _, r := range *l {
		if r.Response != nil {
			responses = append(responses, r.Response)
		}
	}
	return responses
}

func (l *JsonRpcResponses) AddPendingOrLoadFromCache(request *JsonRpcMsg, cache *ttlcache.Cache[uint64, *JsonRpcMsg], ruleCache *RuleCache, cacheKey uint64) {
	res := &JsonRpcResponse{
		Request:  request,
		Cache:    ruleCache,
		CacheKey: cacheKey,
	}
	if res.Cache != nil {
		if cache.Has(res.CacheKey) {
			res.Response = cache.Get(res.CacheKey).Value()
			res.Response.ID = request.ID
		}
	}
	*l = append(*l, res)
}

func (l *JsonRpcResponses) Set(requests, responses JsonRpcMsgs) {
	for i, r := range requests {
		res := l.Find(r)
		if res != nil {
			res.Response = responses[i]
		}
	}
}

func (l *JsonRpcResponses) Find(request *JsonRpcMsg) *JsonRpcResponse {
	for _, r := range *l {
		if r.Request == request {
			return r
		}
	}
	return nil
}

func (l *JsonRpcResponses) Deny(request *JsonRpcMsg) {
	res := &JsonRpcResponse{
		Request:  request,
		Response: UnauthorizedResponse(request),
		Cache:    nil,
		CacheKey: 0,
	}
	*l = append(*l, res)
}

func (l *JsonRpcResponses) StoreInCache(cache *ttlcache.Cache[uint64, *JsonRpcMsg]) {
	for _, r := range *l {
		if r.Response != nil && r.Cache != nil && r.Cache.Enable {
			cache.Set(r.CacheKey, r.Response, r.Cache.TTL)
		}
	}
}
