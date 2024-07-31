# cosmos-firewall

## Introduction
**cosmos-firewall** is a specialized firewall designed for Cosmos nodes. It offers fine-grained control over API access by allowing node administrators to manage access at the API endpoint level, rather than just by port. Additionally, it features a caching mechanism to optimize performance by caching responses for specified endpoints.

## Key Features
- **Endpoint-Level Access Control**: Control access to specific API endpoints across all supported APIs:
    - Tendermint `RPC` (including `JSON-RPC` and `WebSockets`)
    - Cosmos API
    - `gRPC`
    - EVM `JSON-RPC` (including `WebSockets`)
- **Wildcard support**: Use wildcards in firewall rules to define broad or specific access control:
    - `*` matches any single component in a path.
    - `**` matches multiple components, allowing for more flexible rules.
- **Caching**: Define caching strategies for each endpoint to improve performance (`gRPC` not supported). Two caching backends are currently supported:
    - **In-Memory**: Default caching mechanism, storing data in memory.
    - **Redis**: For distributed caching across multiple instances.
- **Rule Prioritization**: Firewall rules can be prioritized using an integer value, with lower numbers indicating higher priority. By default, rules have a priority of 1000, but this can be adjusted to ensure specific rules are evaluated first.
- **WebSocket Connection Management**: The firewall maintains a limited number of WebSocket connections to the node (default is 10). This helps to optimize node resource usage by offloading the handling of thousands of WebSocket connections to the firewall.
- **Hot-Reloading of Configuration**: The configuration file, specifically the firewall rules, can be updated without restarting the application. Changes to the configuration file trigger hot-reloading, recompiling and applying the new rules on-the-fly.


## Installation

### Prerequisites
- **Dependencies**:
    - **Go 1.22**

### Installation Steps

#### Use Docker

An official docker image is available. You can use it by mounting config file at the root path (or use `-config` flag if you want to mount it somewhere else):
```bash
$ docker run -it --name firewall -v /path/to/config/file.yaml:/root/firewall.yaml ghcr.io/nibiruchain/cosmos-firewall --help
Usage of /firewall:
  -config string
    	Path to configuration file. (default "/root/firewall.yaml")
  -log-format string
    	log format (either json or text) (default "json")
  -log-level string
    	log level. (default "info")
  -version
    	print firewall version
```

#### Build from source
1. Clone the repo and install using Makefile:
```bash
$ git clone https://github.com/NibiruChain/cosmos-firewall.git
$ cd cosmos-firewall
$ make install
```

2. Check installation (ensure `~/go/bin` is in your `PATH`):
```bash
$ firewall --help
Usage of firewall:
  -config string
    	Path to configuration file. (default "$HOME/firewall.yaml")
  -log-format string
    	log format (either json or text) (default "json")
  -log-level string
    	log level. (default "info")
  -version
    	print firewall version
```

## Usage Instructions

### Configuration File

The configuration file contains all configurations and firewall rules to be applied. Hot-reloading is active for firewall rules, so that any changes are applied on-the-fly.
All fields are optional, so an empty configuration file is enough to start, but the default is to have no firewall rules (blocks everything).

An example to only allow querying node status on both `RPC` and `JSON-RPC`, and cache the response for 10 seconds, would be:

```yaml
cache:
  ttl: 10s

rpc:
  rules:
    - action: allow
      paths:
        - /status
      methods:
        - GET
      cache:
        enable: true

  jsonrpc:
    rules:
      - action: allow
        methods: [ "status" ]
        cache:
          enable: true
```

Refer to [Configuration](./CONFIG.md) to see all configuration options and their defaults.

### Starting the Firewall
To start the firewall, use the following command:
```
$ firewall -config /path/to/config/file.yaml
```