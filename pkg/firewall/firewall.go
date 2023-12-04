package firewall

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

type Firewall struct {
	cfgFile        string
	cfg            *Config
	lcdProxy       *HttpProxy
	rpcProxy       *HttpProxy
	grpcProxy      *GrpcProxy
	jsonRpcHandler *JsonRpcHandler
	mu             sync.Mutex
}

func New(path string) (*Firewall, error) {
	firewall := &Firewall{cfgFile: path}

	log.WithField("file", path).Info("loading config file")
	if err := firewall.loadConfig(); err != nil {
		return nil, err
	}

	grpcProxy, err := NewGrpcProxy("grpc",
		fmt.Sprintf("%s:%d", firewall.cfg.Host, firewall.cfg.GrpcPort),
		fmt.Sprintf("%s:%d", firewall.cfg.Node.Host, firewall.cfg.Node.GrpcPort),
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up grpc firewall proxy: %v", err)
	}
	firewall.grpcProxy = grpcProxy

	lcdProxy, err := NewHttpProxy("lcd",
		fmt.Sprintf("%s:%d", firewall.cfg.Host, firewall.cfg.LcdPort),
		fmt.Sprintf("http://%s:%d", firewall.cfg.Node.Host, firewall.cfg.Node.LcdPort),
		WithCacheConfig[HttpProxyOptions](&firewall.cfg.Cache),
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up lcd firewall proxy: %v", err)
	}
	firewall.lcdProxy = lcdProxy

	jsonRpcHandler, err := NewJsonRpcHandler(
		WithCacheConfig[JsonRpcHandlerOptions](&firewall.cfg.Cache),
		WithWebSocketEnabled[JsonRpcHandlerOptions](firewall.cfg.RPC.WebSocketEnabled),
		WithWebSocketBackend[JsonRpcHandlerOptions](fmt.Sprintf("%s:%d", firewall.cfg.Node.Host, firewall.cfg.Node.RpcPort)),
		WithWebSocketConnections[JsonRpcHandlerOptions](firewall.cfg.RPC.WebSocketConnections),
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up jsonrpc handler: %v", err)
	}
	firewall.jsonRpcHandler = jsonRpcHandler

	rpcProxy, err := NewHttpProxy("rpc",
		fmt.Sprintf("%s:%d", firewall.cfg.Host, firewall.cfg.RpcPort),
		fmt.Sprintf("http://%s:%d", firewall.cfg.Node.Host, firewall.cfg.Node.RpcPort),
		WithCacheConfig[HttpProxyOptions](&firewall.cfg.Cache),
		WithEndpointHandler[HttpProxyOptions]([]Endpoint{
			{
				Path:   "/",
				Method: "POST",
			},
			{
				Path:   "/websocket",
				Method: "GET",
			},
		}, jsonRpcHandler),
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up rpc firewall proxy: %v", err)
	}
	firewall.rpcProxy = rpcProxy

	return firewall, nil
}

func (f *Firewall) Start() error {
	f.applyRules()
	go func() {
		if err := f.rpcProxy.Start(); err != nil {
			log.Errorf("error starting rpc proxy: %v", err)
		}
	}()
	go func() {
		if err := f.grpcProxy.Start(); err != nil {
			log.Errorf("error starting grpc proxy: %v", err)
		}
	}()
	go func() {
		if err := f.lcdProxy.Start(); err != nil {
			log.Errorf("error starting lcd proxy: %v", err)
		}
	}()
	return f.WatchConfigFile()
}

func (f *Firewall) WatchConfigFile() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := watcher.Add(filepath.Dir(f.cfgFile)); err != nil {
		return err
	}
	for {
		select {
		case _, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("could not retrieve event")
			}
			log.WithField("file", f.cfgFile).Info("reloading config file")
			if err := f.loadConfig(); err != nil {
				return err
			}
			f.applyRules()
		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("could not retrieve error")
			}
			return err
		}
	}
}

func (f *Firewall) loadConfig() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var err error
	f.cfg, err = ReadConfigFromFile(f.cfgFile)
	return err
}

func (f *Firewall) applyRules() {
	f.mu.Lock()
	defer f.mu.Unlock()

	log.Info("applying firewall rules")

	// Rules for LCD
	log.WithField("default", f.cfg.LCD.Default).Debug("applying LCD firewall rules")
	f.lcdProxy.SetRules(f.cfg.LCD.Rules, f.cfg.LCD.Default)

	// Rules for gRPC
	log.WithField("default", f.cfg.GRPC.Default).Debug("applying gRPC firewall rules")
	f.grpcProxy.SetRules(f.cfg.GRPC.Rules, f.cfg.GRPC.Default)

	// Rules for RPC (and jsonrpc)
	log.WithField("default", f.cfg.RPC.Default).Debug("applying RPC firewall rules")
	f.rpcProxy.SetRules(f.cfg.RPC.Rules, f.cfg.RPC.Default)
	log.WithField("default", f.cfg.RPC.JsonRpc.Default).Debug("applying JSONRPC firewall rules")
	f.jsonRpcHandler.SetRules(f.cfg.RPC.JsonRpc.Rules, f.cfg.RPC.JsonRpc.Default)
}
