package firewall

import "net/http"

type WebSocketHandler struct {
}

func NewWebSocketProxy() (*WebSocketHandler, error) {
	// TODO
	return nil, nil
}

func (h *WebSocketHandler) Start() error {
	// TODO
	return nil
}

func (h *WebSocketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// TODO
}
