package cosmoguard

import (
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"

	log "github.com/sirupsen/logrus"

	"github.com/NibiruChain/cosmoguard/pkg/util"
)

type UpstreamPool struct {
	conn    []UpstreamConnManager
	connIdx uint32
	log     *log.Entry
	IdGen   *util.UniqueID

	subscriptionConn  map[string]UpstreamConnManager
	subscriptionID    map[string]string
	subscriptionParam map[string]string
	subMux            sync.Mutex

	onSubscriptionMessage func(*JsonRpcMsg)
}

func NewUpstreamPool(backend, path string, n int, onMessage func(*JsonRpcMsg), upstreamConstructor UpstreamConnManagerConstructor) *UpstreamPool {
	pool := &UpstreamPool{
		conn:                  make([]UpstreamConnManager, n),
		subscriptionConn:      make(map[string]UpstreamConnManager),
		subscriptionID:        make(map[string]string),
		subscriptionParam:     make(map[string]string),
		onSubscriptionMessage: onMessage,
		IdGen:                 &util.UniqueID{},
	}

	backendUrl := url.URL{Scheme: "ws", Host: backend, Path: path}
	for i := 0; i < n; i++ {
		pool.conn[i] = upstreamConstructor(backendUrl, pool.IdGen, pool.onSubscriptionMessage)
	}

	return pool
}

func (p *UpstreamPool) Start(log *log.Entry) error {
	p.log = log
	for i, conn := range p.conn {
		go func(id int, c UpstreamConnManager) {
			if err := c.Run(p.log.WithField("upstream-id", id)); err != nil {
				p.log.Errorf("error on upstream connection: %v", err)
			}
		}(i, conn)
	}
	return nil
}

func (p *UpstreamPool) getConnection() (UpstreamConnManager, error) {
	// TODO: this uses round-robin. Maybe get the connection with least subscriptions.
	// TODO: make sure we return a connection that is not closed
	n := atomic.AddUint32(&p.connIdx, 1)
	return p.conn[(int(n)-1)%len(p.conn)], nil
}

func (p *UpstreamPool) MakeRequest(msg *JsonRpcMsg) (*JsonRpcMsg, error) {
	conn, err := p.getConnection()
	if err != nil {
		return nil, err
	}
	return conn.MakeRequest(msg)
}

func (p *UpstreamPool) Subscribe(param string) (string, error) {
	p.subMux.Lock()
	defer p.subMux.Unlock()

	// Check if this param is subscribed already
	if id, ok := p.subscriptionID[param]; ok {
		return id, nil
	}

	conn, err := p.getConnection()
	if err != nil {
		return "", err
	}

	id, err := conn.Subscribe(param)
	if err != nil {
		return "", err
	}

	p.subscriptionParam[id] = param
	p.subscriptionID[param] = id
	p.subscriptionConn[id] = conn
	return id, nil
}

func (p *UpstreamPool) Unsubscribe(subID string) error {
	p.subMux.Lock()
	defer p.subMux.Unlock()

	conn, ok := p.subscriptionConn[subID]
	if !ok {
		return fmt.Errorf("connection for subscription not found")
	}
	if err := conn.Unsubscribe(subID); err != nil {
		return err
	}

	delete(p.subscriptionConn, subID)
	delete(p.subscriptionID, p.subscriptionParam[subID])
	delete(p.subscriptionParam, subID)
	return nil
}
