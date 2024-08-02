package cosmoguard

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

type CosmoGuard struct {
	cfgFile     string
	cfg         *Config
	configMutex sync.Mutex

	lcdProxy       *HttpProxy
	rpcProxy       *HttpProxy
	grpcProxy      *GrpcProxy
	jsonRpcHandler *JsonRpcHandler

	evmRpcProxy         *HttpProxy
	evmRpcWsProxy       *HttpProxy
	evmJsonRpcHandler   *JsonRpcHandler
	evmJsonRpcWsHandler *JsonRpcHandler
}

func New(path string) (*CosmoGuard, error) {
	cosmoGuard := &CosmoGuard{cfgFile: path}

	log.WithField("file", path).Info("loading config file")
	err := cosmoGuard.loadConfig()
	if err != nil {
		return nil, err
	}

	// Setup gRPC proxy
	cosmoGuard.grpcProxy, err = NewGrpcProxy("grpc",
		fmt.Sprintf("%s:%d", cosmoGuard.cfg.Host, cosmoGuard.cfg.GrpcPort),
		fmt.Sprintf("%s:%d", cosmoGuard.cfg.Node.Host, cosmoGuard.cfg.Node.GrpcPort),
		WithMetricsEnabled[GrpcProxyOptions](cosmoGuard.cfg.Metrics.Enable),
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up grpc cosmoguard proxy: %v", err)
	}

	// Setup LCD proxy
	cosmoGuard.lcdProxy, err = NewHttpProxy("lcd",
		fmt.Sprintf("%s:%d", cosmoGuard.cfg.Host, cosmoGuard.cfg.LcdPort),
		fmt.Sprintf("http://%s:%d", cosmoGuard.cfg.Node.Host, cosmoGuard.cfg.Node.LcdPort),
		WithCacheConfig[HttpProxyOptions](&cosmoGuard.cfg.Cache),
		WithMetricsEnabled[HttpProxyOptions](cosmoGuard.cfg.Metrics.Enable),
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up lcd cosmoguard proxy: %v", err)
	}

	// Setup JSONRPC handler for RPC proxy
	cosmoGuard.jsonRpcHandler, err = NewJsonRpcHandler("jsonrpc",
		WithCacheConfig[JsonRpcHandlerOptions](&cosmoGuard.cfg.Cache),
		WithWebSocketEnabled[JsonRpcHandlerOptions](cosmoGuard.cfg.RPC.WebSocketEnabled),
		WithWebSocketBackend[JsonRpcHandlerOptions](fmt.Sprintf("%s:%d", cosmoGuard.cfg.Node.Host, cosmoGuard.cfg.Node.RpcPort)),
		WithWebSocketConnections[JsonRpcHandlerOptions](cosmoGuard.cfg.RPC.WebSocketConnections),
		WithMetricsEnabled[JsonRpcHandlerOptions](cosmoGuard.cfg.Metrics.Enable),
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up jsonrpc handler: %v", err)
	}

	// Setup RPC proxy
	cosmoGuard.rpcProxy, err = NewHttpProxy("rpc",
		fmt.Sprintf("%s:%d", cosmoGuard.cfg.Host, cosmoGuard.cfg.RpcPort),
		fmt.Sprintf("http://%s:%d", cosmoGuard.cfg.Node.Host, cosmoGuard.cfg.Node.RpcPort),
		WithCacheConfig[HttpProxyOptions](&cosmoGuard.cfg.Cache),
		WithMetricsEnabled[HttpProxyOptions](cosmoGuard.cfg.Metrics.Enable),
		WithEndpointHandler[HttpProxyOptions]([]Endpoint{
			{
				Path:   "/",
				Method: "POST",
			},
			{
				Path:   defaultWebsocketPath,
				Method: "GET",
			},
		}, cosmoGuard.jsonRpcHandler),
	)
	if err != nil {
		return nil, fmt.Errorf("error setting up rpc cosmoguard proxy: %v", err)
	}

	if cosmoGuard.cfg.EnableEvm {
		// Setup JSONRPC handler for EVM RPC proxy
		cosmoGuard.evmJsonRpcHandler, err = NewJsonRpcHandler("evm_jsonrpc",
			WithCacheConfig[JsonRpcHandlerOptions](&cosmoGuard.cfg.Cache),
			WithWebSocketEnabled[JsonRpcHandlerOptions](false),
			WithMetricsEnabled[JsonRpcHandlerOptions](cosmoGuard.cfg.Metrics.Enable),
		)
		if err != nil {
			return nil, fmt.Errorf("error setting up jsonrpc handler for evm-rpc: %v", err)
		}

		cosmoGuard.evmRpcProxy, err = NewHttpProxy("evm_rpc",
			fmt.Sprintf("%s:%d", cosmoGuard.cfg.Host, cosmoGuard.cfg.EvmRpcPort),
			fmt.Sprintf("http://%s:%d", cosmoGuard.cfg.Node.Host, cosmoGuard.cfg.Node.EvmRpcPort),
			WithCacheConfig[HttpProxyOptions](&cosmoGuard.cfg.Cache),
			WithMetricsEnabled[HttpProxyOptions](cosmoGuard.cfg.Metrics.Enable),
			WithEndpointHandler[HttpProxyOptions]([]Endpoint{
				{
					Path:   "/",
					Method: "POST",
				},
			}, cosmoGuard.evmJsonRpcHandler),
		)
		if err != nil {
			return nil, fmt.Errorf("error setting up evm-rpc cosmoguard proxy: %v", err)
		}

		// Setup JSONRPC handler for EVM RPC WS proxy
		cosmoGuard.evmJsonRpcWsHandler, err = NewJsonRpcHandler("evm_jsonrpc_ws",
			WithCacheConfig[JsonRpcHandlerOptions](&cosmoGuard.cfg.Cache),
			WithWebSocketEnabled[JsonRpcHandlerOptions](true),
			WithWebSocketConnections[JsonRpcHandlerOptions](cosmoGuard.cfg.EVM.WS.WebSocketConnections),
			WithWebSocketBackend[JsonRpcHandlerOptions](fmt.Sprintf("%s:%d", cosmoGuard.cfg.Node.Host, cosmoGuard.cfg.Node.EvmRpcWsPort)),
			WithWebSocketPath[JsonRpcHandlerOptions]("/"),
			WithUpstreamManager[JsonRpcHandlerOptions](EthUpstreamConnManager),
			WithMetricsEnabled[JsonRpcHandlerOptions](cosmoGuard.cfg.Metrics.Enable),
		)
		if err != nil {
			return nil, fmt.Errorf("error setting up jsonrpc handler for evm-rpc: %v", err)
		}

		cosmoGuard.evmRpcWsProxy, err = NewHttpProxy("evm_rpc_ws",
			fmt.Sprintf("%s:%d", cosmoGuard.cfg.Host, cosmoGuard.cfg.EvmRpcWsPort),
			fmt.Sprintf("http://%s:%d", cosmoGuard.cfg.Node.Host, cosmoGuard.cfg.Node.EvmRpcWsPort),
			WithCacheConfig[HttpProxyOptions](&cosmoGuard.cfg.Cache),
			WithMetricsEnabled[HttpProxyOptions](cosmoGuard.cfg.Metrics.Enable),
			WithEndpointHandler[HttpProxyOptions]([]Endpoint{
				{
					Path:   "/",
					Method: "GET",
				},
			}, cosmoGuard.evmJsonRpcWsHandler),
		)
		if err != nil {
			return nil, fmt.Errorf("error setting up evm-rpc-ws cosmoguard proxy: %v", err)
		}
	}

	return cosmoGuard, nil
}

func (f *CosmoGuard) Run() error {
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

	if f.cfg.EnableEvm {
		go func() {
			if err := f.evmRpcProxy.Run(); err != nil {
				log.Errorf("error on evm-rpc proxy: %v", err)
			}
		}()

		go func() {
			if err := f.evmRpcWsProxy.Run(); err != nil {
				log.Errorf("error on evm-rpc-ws proxy: %v", err)
			}
		}()
	}

	return f.WatchConfigFile()
}

func (f *CosmoGuard) WatchConfigFile() error {
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

func (f *CosmoGuard) loadConfig() error {
	f.configMutex.Lock()
	defer f.configMutex.Unlock()

	var err error
	f.cfg, err = ReadConfigFromFile(f.cfgFile)
	return err
}

func (f *CosmoGuard) applyRules() {
	f.configMutex.Lock()
	defer f.configMutex.Unlock()

	log.Info("applying cosmoguard rules")

	// Rules for LCD
	log.WithField("default", f.cfg.LCD.Default).Debug("applying LCD cosmoguard rules")
	f.lcdProxy.SetRules(f.cfg.LCD.Rules, f.cfg.LCD.Default)

	// Rules for gRPC
	log.WithField("default", f.cfg.GRPC.Default).Debug("applying gRPC cosmoguard rules")
	f.grpcProxy.SetRules(f.cfg.GRPC.Rules, f.cfg.GRPC.Default)

	// Rules for RPC (and jsonrpc)
	log.WithField("default", f.cfg.RPC.Default).Debug("applying RPC cosmoguard rules")
	f.rpcProxy.SetRules(f.cfg.RPC.Rules, f.cfg.RPC.Default)
	log.WithField("default", f.cfg.RPC.JsonRpc.Default).Debug("applying JSONRPC cosmoguard rules")
	f.jsonRpcHandler.SetRules(f.cfg.RPC.JsonRpc.Rules, f.cfg.RPC.JsonRpc.Default)

	if f.cfg.EnableEvm {
		// Rules for EVM RPC
		log.WithField("default", f.cfg.RPC.Default).Debug("applying EVM-RPC cosmoguard rules")
		f.evmJsonRpcHandler.SetRules(f.cfg.EVM.RPC.Rules, f.cfg.EVM.RPC.Default)

		// Rules for EVM RPC WS
		log.WithField("default", f.cfg.RPC.Default).Debug("applying EVM-RPC-WS cosmoguard rules")
		f.evmJsonRpcWsHandler.SetRules(f.cfg.EVM.WS.Rules, f.cfg.EVM.WS.Default)
	}
}
