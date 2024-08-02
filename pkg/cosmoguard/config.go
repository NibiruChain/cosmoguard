package cosmoguard

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/creasty/defaults"
	"gopkg.in/yaml.v3"
)

const (
	cacheKey  = "Cache"
	cacheHit  = "hit"
	cacheMiss = "miss"
)

type Config struct {
	Host         string            `yaml:"host,omitempty" default:"0.0.0.0"`
	RpcPort      int               `yaml:"rpcPort,omitempty" default:"16657"`
	LcdPort      int               `yaml:"lcdPort,omitempty" default:"11317"`
	GrpcPort     int               `yaml:"grpcPort,omitempty" default:"19090"`
	EnableEvm    bool              `yaml:"enableEvm,omitempty" default:"false"`
	EvmRpcPort   int               `yaml:"evmRpcPort,omitempty" default:"18545"`
	EvmRpcWsPort int               `yaml:"evmRpcWsPort,omitempty" default:"18546"`
	Node         NodeConfig        `yaml:"node,omitempty"`
	Cache        CacheGlobalConfig `yaml:"cache,omitempty"`
	LCD          LcdConfig         `yaml:"lcd,omitempty"`
	RPC          RpcConfig         `yaml:"rpc,omitempty"`
	GRPC         GrpcConfig        `yaml:"grpc,omitempty"`
	EVM          EvmConfig         `yaml:"evm,omitempty"`
	Metrics      MetricsConfig     `yaml:"metrics,omitempty"`
}

type NodeConfig struct {
	Host         string `yaml:"host,omitempty" default:"127.0.0.1"`
	RpcPort      int    `yaml:"rpcPort,omitempty" default:"26657"`
	LcdPort      int    `yaml:"lcdPort,omitempty" default:"1317"`
	GrpcPort     int    `yaml:"grpcPort,omitempty" default:"9090"`
	EvmRpcPort   int    `yaml:"evmRpcPort,omitempty" default:"8545"`
	EvmRpcWsPort int    `yaml:"evmRpcWsPort,omitempty" default:"8546"`
}

type CacheGlobalConfig struct {
	TTL   time.Duration `yaml:"ttl,omitempty" default:"5s"`
	Redis *string       `yaml:"redis,omitempty"`
}

type RuleCache struct {
	Enable bool          `yaml:"enable,omitempty"`
	TTL    time.Duration `yaml:"ttl,omitempty"`
}

type LcdConfig struct {
	Default RuleAction  `yaml:"default,omitempty" default:"deny"`
	Rules   []*HttpRule `yaml:"rules,omitempty"`
}

type RpcConfig struct {
	Default              RuleAction    `yaml:"default,omitempty" default:"deny"`
	Rules                []*HttpRule   `yaml:"rules,omitempty"`
	JsonRpc              JsonRpcConfig `yaml:"jsonrpc,omitempty"`
	WebSocketEnabled     bool          `yaml:"webSocketEnabled,omitempty" default:"true"`
	WebSocketConnections int           `yaml:"webSocketConnections,omitempty" default:"10"`
}

type JsonRpcConfig struct {
	Default RuleAction     `yaml:"default,omitempty" default:"deny"`
	Rules   []*JsonRpcRule `yaml:"rules,omitempty"`
}

type GrpcConfig struct {
	Default RuleAction  `yaml:"default,omitempty" default:"deny"`
	Rules   []*GrpcRule `yaml:"rules,omitempty"`
}

type MetricsConfig struct {
	Enable bool `yaml:"enable" default:"true"`
	Port   int  `yaml:"port,omitempty" default:"9001"`
}

type EvmConfig struct {
	RPC EvmRpcConfig   `yaml:"rpc,omitempty"`
	WS  EvmRpcWsConfig `yaml:"ws,omitempty"`
}

type EvmRpcConfig struct {
	Default RuleAction     `yaml:"default,omitempty" default:"deny"`
	Rules   []*JsonRpcRule `yaml:"rules,omitempty"`
}

type EvmRpcWsConfig struct {
	Default              RuleAction     `yaml:"default,omitempty" default:"deny"`
	Rules                []*JsonRpcRule `yaml:"rules,omitempty"`
	WebSocketConnections int            `yaml:"webSocketConnections,omitempty" default:"10"`
}

func ReadConfigFromFile(path string) (*Config, error) {
	f, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}
	var cfg Config
	err = yaml.Unmarshal(f, &cfg)
	if err != nil {
		return nil, fmt.Errorf("error in config file unmarshal: %v", err)
	}

	// Sort rules by priority and compile rules
	if cfg.LCD.Rules != nil {
		sort.Slice(cfg.LCD.Rules, func(i, j int) bool {
			return cfg.LCD.Rules[i].Priority < cfg.LCD.Rules[j].Priority
		})
		for _, rule := range cfg.LCD.Rules {
			rule.Compile()
		}
	}
	if cfg.GRPC.Rules != nil {
		sort.Slice(cfg.GRPC.Rules, func(i, j int) bool {
			return cfg.GRPC.Rules[i].Priority < cfg.GRPC.Rules[j].Priority
		})
		for _, rule := range cfg.GRPC.Rules {
			rule.Compile()
		}
	}

	if cfg.RPC.Rules != nil {
		sort.Slice(cfg.RPC.Rules, func(i, j int) bool {
			return cfg.RPC.Rules[i].Priority < cfg.RPC.Rules[j].Priority
		})
	}
	for _, rule := range cfg.RPC.Rules {
		rule.Compile()
	}

	if cfg.RPC.JsonRpc.Rules != nil {
		sort.Slice(cfg.RPC.JsonRpc.Rules, func(i, j int) bool {
			return cfg.RPC.JsonRpc.Rules[i].Priority < cfg.RPC.JsonRpc.Rules[j].Priority
		})
		for _, rule := range cfg.RPC.JsonRpc.Rules {
			rule.Compile()
		}
	}

	if cfg.EVM.RPC.Rules != nil {
		sort.Slice(cfg.EVM.RPC.Rules, func(i, j int) bool {
			return cfg.EVM.RPC.Rules[i].Priority < cfg.EVM.RPC.Rules[j].Priority
		})
		for _, rule := range cfg.EVM.RPC.Rules {
			rule.Compile()
		}
	}

	if cfg.EVM.WS.Rules != nil {
		sort.Slice(cfg.EVM.WS.Rules, func(i, j int) bool {
			return cfg.EVM.WS.Rules[i].Priority < cfg.EVM.WS.Rules[j].Priority
		})
		for _, rule := range cfg.EVM.WS.Rules {
			rule.Compile()
		}
	}

	return &cfg, defaults.Set(&cfg)
}
