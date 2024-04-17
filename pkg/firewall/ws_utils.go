package firewall

import "fmt"

const (
	methodSubscribe      = "subscribe"
	methodUnsubscribe    = "unsubscribe"
	methodUnsubscribeAll = "unsubscribe_all"
)

func hasSubscriptionMethod(request *JsonRpcMsg) bool {
	if request.Method == methodSubscribe ||
		request.Method == methodUnsubscribe ||
		request.Method == methodUnsubscribeAll {
		return true
	}
	return false
}

func getSubscriptionQuery(req *JsonRpcMsg) (string, error) {
	if params, ok := req.Params.(map[string]interface{}); ok {
		if query, ok := params["query"].(string); ok {
			return query, nil
		}
	}

	params, ok := req.Params.([]interface{})
	if !ok {
		return "", fmt.Errorf("bad params for subscribe")
	}

	query, ok := params[0].(string)

	if !ok {
		return "", fmt.Errorf("bad query format (should be string)")
	}

	return query, nil
}
