package firewall

import (
	"fmt"
	"strings"
)

const (
	methodSubscribeCosmos      = "subscribe"
	methodUnsubscribeCosmos    = "unsubscribe"
	methodUnsubscribeAllCosmos = "unsubscribe_all"
	methodSubscribeEth         = "eth_subscribe"
	methodUnsubscribeEth       = "eth_unsubscribe"
)

func hasSubscriptionMethod(request *JsonRpcMsg) bool {
	if request.Method == methodSubscribeCosmos ||
		request.Method == methodUnsubscribeCosmos ||
		request.Method == methodUnsubscribeAllCosmos ||
		request.Method == methodSubscribeEth ||
		request.Method == methodUnsubscribeEth {
		return true
	}
	return false
}

func getSubscriptionParam(req *JsonRpcMsg) (string, error) {
	// Try to get it from dictionary with query key (cosmos only)
	if params, ok := req.Params.(map[string]interface{}); ok {
		if query, ok := params["query"].(string); ok {
			return query, nil
		}
	}

	// Otherwise, get from array of values (Both cosmos and eth)
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

func isEthSubscriptionID(params string) bool {
	return strings.HasPrefix(params, "0x")
}
