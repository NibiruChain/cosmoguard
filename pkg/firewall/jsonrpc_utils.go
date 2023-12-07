package firewall

import (
	"bytes"
	"hash/maphash"
	"net/http"
	"strconv"

	"github.com/jellydator/ttlcache/v3"
	jsoniter "github.com/json-iterator/go"
)

var (
	hasherSeed = maphash.MakeSeed()
	json       = jsoniter.ConfigCompatibleWithStandardLibrary
)

type JsonRpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (e *JsonRpcError) Clone() *JsonRpcError {
	if e == nil {
		return nil
	}
	return &JsonRpcError{
		Code:    e.Code,
		Message: e.Message,
		Data:    e.Data,
	}
}

type JsonRpcMsg struct {
	Version string        `json:"jsonrpc"`
	ID      interface{}   `json:"id,omitempty"`
	Method  string        `json:"method,omitempty"`
	Params  interface{}   `json:"params,omitempty"`
	Result  interface{}   `json:"result,omitempty"`
	Error   *JsonRpcError `json:"error,omitempty"`
}

func (j *JsonRpcMsg) UnmarshalJSON(b []byte) error {
	type msg JsonRpcMsg

	var dest = struct {
		*msg
		ID jsoniter.RawMessage `json:"id,omitempty"`
	}{
		msg: (*msg)(j),
	}

	if err := json.Unmarshal(b, &dest); err != nil {
		return err
	}

	i, err := strconv.Atoi(string(dest.ID))
	if err == nil {
		j.ID = i
	} else {
		j.ID = string(dest.ID)
	}

	return nil
}

func (j *JsonRpcMsg) Clone() *JsonRpcMsg {
	return &JsonRpcMsg{
		Version: j.Version,
		ID:      j.ID,
		Method:  j.Method,
		Params:  j.Params,
		Result:  j.Result,
		Error:   j.Error.Clone(),
	}
}

func (j *JsonRpcMsg) CloneWithID(id interface{}) *JsonRpcMsg {
	return &JsonRpcMsg{
		Version: j.Version,
		ID:      id,
		Method:  j.Method,
		Params:  j.Params,
		Result:  j.Result,
		Error:   j.Error.Clone(),
	}
}

func (j *JsonRpcMsg) Hash() uint64 {
	b, err := json.Marshal(j.Params)
	if err != nil {
		return 0
	}
	return maphash.Bytes(hasherSeed, append([]byte(j.Method), b...))
}

func (j *JsonRpcMsg) Marshal() ([]byte, error) {
	return json.Marshal(j)
}

func (j *JsonRpcMsg) MaybeGetPath() string {
	path := ""
	if m, ok := j.Params.(map[string]interface{}); ok {
		if v, ok := m["path"]; ok {
			path, _ = v.(string)
		}
	}
	return path
}

type JsonRpcMsgs []*JsonRpcMsg

func (j JsonRpcMsgs) Marshal() ([]byte, error) {
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

func EmptyResult(req *JsonRpcMsg) *JsonRpcMsg {
	return &JsonRpcMsg{
		Version: "2.0",
		Result:  make(map[string]string),
		ID:      req.ID,
	}
}

func ErrorResponse(req *JsonRpcMsg, code int, message string, data interface{}) *JsonRpcMsg {
	return &JsonRpcMsg{
		Version: "2.0",
		Error: &JsonRpcError{
			Code:    code,
			Message: message,
			Data:    data,
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

func (l *JsonRpcResponses) AddPendingOrLoadFromCache(request *JsonRpcMsg, cache *ttlcache.Cache[uint64, *JsonRpcMsg], ruleCache *RuleCache, cacheKey uint64) (hit bool) {
	res := &JsonRpcResponse{
		Request:  request,
		Cache:    ruleCache,
		CacheKey: cacheKey,
	}
	if res.Cache != nil {
		if cache.Has(res.CacheKey) {
			hit = true
			res.Response = cache.Get(res.CacheKey).Value()
			res.Response.ID = request.ID
		}
	}
	*l = append(*l, res)
	return
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
