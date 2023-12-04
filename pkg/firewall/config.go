package firewall

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/creasty/defaults"
	"gopkg.in/yaml.v3"
)

const (
	defaultCacheTTL = time.Minute
)

type Config struct {
	Host     string            `yaml:"host,omitempty" default:"0.0.0.0"`
	RpcPort  int               `yaml:"rpcPort,omitempty" default:"26657"`
	LcdPort  int               `yaml:"lcdPort,omitempty" default:"1317"`
	GrpcPort int               `yaml:"grpcPort,omitempty" default:"9090"`
	Node     NodeConfig        `yaml:"node,omitempty"`
	Cache    CacheGlobalConfig `yaml:"cache,omitempty"`
	LCD      LcdConfig         `yaml:"lcd,omitempty"`
	RPC      RpcConfig         `yaml:"rpc,omitempty"`
	GRPC     GrpcConfig        `yaml:"grpc,omitempty"`
}

type NodeConfig struct {
	Host     string `yaml:"host,omitempty" default:"127.0.0.1"`
	RpcPort  int    `yaml:"rpcPort,omitempty" default:"26657"`
	LcdPort  int    `yaml:"lcdPort,omitempty" default:"1317"`
	GrpcPort int    `yaml:"grpcPort,omitempty" default:"9090"`
}

type CacheGlobalConfig struct {
	TTL        time.Duration `yaml:"ttl,omitempty" default:"1m"`
	TouchOnHit bool          `yaml:"touchOnHit,omitempty"`
}

type RuleCache struct {
	Enable bool          `yaml:"enable,omitempty"`
	TTL    time.Duration `yaml:"ttl,omitempty"`
}

type LcdConfig struct {
	Default RuleAction  `yaml:"default,omitempty" default:"allow"`
	Rules   []*HttpRule `yaml:"rules,omitempty"`
}

type RpcConfig struct {
	Default              RuleAction    `yaml:"default,omitempty" default:"allow"`
	Rules                []*HttpRule   `yaml:"rules,omitempty"`
	JsonRpc              JsonRpcConfig `yaml:"jsonrpc,omitempty"`
	WebSocketEnabled     bool          `yaml:"webSocketEnabled,omitempty" default:"true"`
	WebSocketConnections int           `yaml:"webSocketConnections,omitempty" default:"10"`
}

type JsonRpcConfig struct {
	Default RuleAction     `yaml:"default,omitempty" default:"allow"`
	Rules   []*JsonRpcRule `yaml:"rules,omitempty"`
}

type GrpcConfig struct {
	Default RuleAction  `yaml:"default,omitempty" default:"allow"`
	Rules   []*GrpcRule `yaml:"rules,omitempty"`
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

	return &cfg, defaults.Set(&cfg)
}
