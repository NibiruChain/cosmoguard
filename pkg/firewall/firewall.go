package firewall

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

type Firewall struct {
	cfgFile        string
	cfg            *Config
	lcdProxy       *HttpProxy
	rpcProxy       *HttpProxy
	grpcProxy      *GrpcProxy
	jsonRpcHandler *JsonRpcHandler
	configMutex    sync.Mutex
}

func New(path string) (*Firewall, error) {
	firewall := &Firewall{cfgFile: path}

	log.WithField("file", path).Info("loading config file")
	if err := firewall.loadConfig(); err != nil {
		return nil, err
	}

	// Setup gRPC proxy
	grpcProxy, err := NewGrpcProxy("grpc",
		fmt.Sprintf("%s:%d", firewall.cfg.Host, firewall.cfg.GrpcPort),
		fmt.Sprintf("%s:%d", firewall.cfg.Node.Host, firewall.cfg.Node.GrpcPort),
		WithMetricsEnabled[GrpcProxyOptions](firewall.cfg.Metrics.Enable),
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up grpc firewall proxy: %v", err)
	}
	firewall.grpcProxy = grpcProxy

	// Setup LCD proxy
	lcdProxy, err := NewHttpProxy("lcd",
		fmt.Sprintf("%s:%d", firewall.cfg.Host, firewall.cfg.LcdPort),
		fmt.Sprintf("http://%s:%d", firewall.cfg.Node.Host, firewall.cfg.Node.LcdPort),
		WithCacheConfig[HttpProxyOptions](&firewall.cfg.Cache),
		WithMetricsEnabled[HttpProxyOptions](firewall.cfg.Metrics.Enable),
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up lcd firewall proxy: %v", err)
	}
	firewall.lcdProxy = lcdProxy

	// Setup JSONRPC handler for RPC proxy
	jsonRpcHandler, err := NewJsonRpcHandler(
		WithCacheConfig[JsonRpcHandlerOptions](&firewall.cfg.Cache),
		WithWebSocketEnabled[JsonRpcHandlerOptions](firewall.cfg.RPC.WebSocketEnabled),
		WithWebSocketBackend[JsonRpcHandlerOptions](fmt.Sprintf("%s:%d", firewall.cfg.Node.Host, firewall.cfg.Node.RpcPort)),
		WithWebSocketConnections[JsonRpcHandlerOptions](firewall.cfg.RPC.WebSocketConnections),
		WithMetricsEnabled[JsonRpcHandlerOptions](firewall.cfg.Metrics.Enable),
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up jsonrpc handler: %v", err)
	}
	firewall.jsonRpcHandler = jsonRpcHandler

	// Setup RPC proxy
	rpcProxy, err := NewHttpProxy("rpc",
		fmt.Sprintf("%s:%d", firewall.cfg.Host, firewall.cfg.RpcPort),
		fmt.Sprintf("http://%s:%d", firewall.cfg.Node.Host, firewall.cfg.Node.RpcPort),
		WithCacheConfig[HttpProxyOptions](&firewall.cfg.Cache),
		WithMetricsEnabled[HttpProxyOptions](firewall.cfg.Metrics.Enable),
		WithEndpointHandler[HttpProxyOptions]([]Endpoint{
			{
				Path:   "/",
				Method: "POST",
			},
			{
				Path:   websocketPath,
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

func (f *Firewall) Run() error {
	f.applyRules()

	if f.cfg.Metrics.Enable {
		go func() {
			log.WithField("address", fmt.Sprintf("%s:%d", f.cfg.Host, f.cfg.Metrics.Port)).
				Info("starting metrics server ")
			http.Handle("/metrics", promhttp.Handler())
			if err := http.ListenAndServe(
				fmt.Sprintf("%s:%d", f.cfg.Host, f.cfg.Metrics.Port),
				nil,
			); err != nil {
				log.Errorf("error starting metrics server: %v", err)
			}
		}()
	}

	go func() {
		if err := f.rpcProxy.Run(); err != nil {
			log.Errorf("error on rpc proxy: %v", err)
		}
	}()

	go func() {
		if err := f.grpcProxy.Run(); err != nil {
			log.Errorf("error on grpc proxy: %v", err)
		}
	}()

	go func() {
		if err := f.lcdProxy.Run(); err != nil {
			log.Errorf("error on lcd proxy: %v", err)
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
	f.configMutex.Lock()
	defer f.configMutex.Unlock()

	var err error
	f.cfg, err = ReadConfigFromFile(f.cfgFile)
	return err
}

func (f *Firewall) applyRules() {
	f.configMutex.Lock()
	defer f.configMutex.Unlock()

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
