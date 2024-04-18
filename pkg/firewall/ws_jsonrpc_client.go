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
	conn   *websocket.Conn
	closed bool

	closedMux sync.RWMutex
	writeMux  sync.Mutex
	readMux   sync.Mutex

	onDisconnect func(client *JsonRpcWsClient)
}

func NewJsonRpcWsClient(conn *websocket.Conn) *JsonRpcWsClient {
	return &JsonRpcWsClient{
		conn:   conn,
		closed: false,
	}
}

func (c *JsonRpcWsClient) readMessage() (int, []byte, error) {
	c.readMux.Lock()
	defer c.readMux.Unlock()
	return c.conn.ReadMessage()
}

func (c *JsonRpcWsClient) writeMessage(messageType int, data []byte) error {
	c.writeMux.Lock()
	defer c.writeMux.Unlock()
	return c.conn.WriteMessage(messageType, data)
}

func (c *JsonRpcWsClient) setClosed() {
	c.closedMux.Lock()
	defer c.closedMux.Unlock()
	c.closed = true
}

func (c *JsonRpcWsClient) IsClosed() bool {
	c.closedMux.RLock()
	defer c.closedMux.RUnlock()
	return c.closed
}

func (c *JsonRpcWsClient) ReceiveMsg() (*JsonRpcMsg, error) {
	if c.IsClosed() {
		return nil, ErrClosed
	}

	_, message, err := c.readMessage()

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
	if c.IsClosed() {
		return ErrClosed
	}

	b, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("error encoding msg: %v", err)
	}

	return c.writeMessage(websocket.TextMessage, b)
}

func (c *JsonRpcWsClient) Close() error {
	if c.IsClosed() {
		return ErrClosed
	}

	if c.onDisconnect != nil {
		c.onDisconnect(c)
	}

	c.writeMux.Lock()
	defer c.writeMux.Unlock()

	if err := c.conn.Close(); err != nil {
		return err
	}

	c.setClosed()
	return nil
}

func (c *JsonRpcWsClient) SetOnDisconnectCallback(f func(client *JsonRpcWsClient)) {
	c.onDisconnect = f
}
