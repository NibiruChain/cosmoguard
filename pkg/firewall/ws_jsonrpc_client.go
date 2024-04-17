package firewall

import (
	"errors"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
)

var (
	ErrClosed = errors.New("websocket client closed")
)

type JsonRpcWsClient struct {
	conn       *websocket.Conn
	closed     bool
	writeMutex sync.Mutex
	readMutex  sync.Mutex

	onDisconnect func(client *JsonRpcWsClient)
}

func NewJsonRpcWsClient(conn *websocket.Conn) *JsonRpcWsClient {
	return &JsonRpcWsClient{
		conn:   conn,
		closed: false,
	}
}

func (c *JsonRpcWsClient) ReceiveMsg() (*JsonRpcMsg, error) {
	if c.closed {
		return nil, ErrClosed
	}

	c.readMutex.Lock()
	_, message, err := c.conn.ReadMessage()
	c.readMutex.Unlock()

	if err != nil {
		if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
			return nil, err
		}
		_ = c.Close()
		return nil, ErrClosed
	}

	if string(message) == "" {
		return nil, fmt.Errorf("received empty json rpc message")
	}

	msg, _, err := ParseJsonRpcMessage(message)
	if err != nil {
		return nil, fmt.Errorf("error decoding msg: %v", err)
	}

	if msg == nil {
		return nil, fmt.Errorf("bad request: %s", string(message))
	}

	return msg, nil
}

func (c *JsonRpcWsClient) SendMsg(msg *JsonRpcMsg) error {
	if c.closed {
		return ErrClosed
	}

	b, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("error encoding msg: %v", err)
	}

	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, b)
}

func (c *JsonRpcWsClient) Close() error {
	if c.closed {
		return ErrClosed
	}

	if c.onDisconnect != nil {
		c.onDisconnect(c)
	}

	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()

	if err := c.conn.Close(); err != nil {
		return err
	}
	c.closed = true
	c.conn = nil
	return nil
}

func (c *JsonRpcWsClient) IsClosed() bool {
	return c.closed
}

func (c *JsonRpcWsClient) SetOnDisconnectCallback(f func(client *JsonRpcWsClient)) {
	c.onDisconnect = f
}
