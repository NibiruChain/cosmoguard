# Configuration

## Overview

The configuration file for CosmoGuard allows fine-grained control over various aspects,
including host settings, ports, caching, metrics, and specific rules for LCD, gRPC, RPC, JSON-RPC, and EVM.
Most fields are optional and default to specified values.

## Configuration Options

| Option                                | Default Value     | Description                                                                                                         |
|---------------------------------------|-------------------|---------------------------------------------------------------------------------------------------------------------|
| **Global Settings**                   |                   |                                                                                                                     |
| `host`                                | `0.0.0.0`         | The host address the CosmoGuard listens on.                                                                         |
| `rpcPort`                             | `16657`           | Port for Tendermint RPC.                                                                                            |
| `lcdPort`                             | `11317`           | Port for Cosmos REST API (LCD).                                                                                     |
| `grpcPort`                            | `19090`           | Port for gRPC.                                                                                                      |
| `enableEvm`                           | `false`           | Enable or disable EVM support.                                                                                      |
| `evmRpcPort`                          | `18545`           | Port for EVM JSON-RPC.                                                                                              |
| `evmRpcWsPort`                        | `18546`           | Port for EVM WebSocket.                                                                                             |
| **Node Settings**                     |                   |                                                                                                                     |
| `node.host`                           | `127.0.0.1`       | The node's host address.                                                                                            |
| `node.rpcPort`                        | `26657`           | Port for the node's Tendermint RPC.                                                                                 |
| `node.lcdPort`                        | `1317`            | Port for the node's LCD.                                                                                            |
| `node.grpcPort`                       | `9090`            | Port for the node's gRPC.                                                                                           |
| `node.evmRpcPort`                     | `8545`            | Port for the node's EVM JSON-RPC.                                                                                   |
| `node.evmRpcWsPort`                   | `8546`            | Port for the node's EVM WebSocket.                                                                                  |
| **Caching Settings**                  |                   |                                                                                                                     |
| `cache.ttl`                           | `5s`              | Global time-to-live for cached responses.                                                                           |
| `cache.redis`                         | (In-memory cache) | Connection URL for Redis if used for caching.                                                                       |
| `cache.redis-sentinel.master_name`    |                   | The master name for Redis Sentinel.                                                                                 |
| `cache.redis-sentinel.sentinel_addrs` |                   | A list of sentinel addresses for Redis Sentinel.                                                                    |
| `cache.key`                           |                   | Optional key prefix for cache. Useful for splitting different caches in same database. Not used on in-memory cache. |
| **Metrics Settings**                  |                   |                                                                                                                     |
| `metrics.enable`                      | `true`            | Enable or disable metrics collection.                                                                               |
| `metrics.port`                        | `9001`            | Port for metrics.                                                                                                   |
| **LCD (REST API) Settings**           |                   |                                                                                                                     |
| `lcd.default`                         | `deny`            | Default action for LCD requests.                                                                                    |
| `lcd.rules`                           | (Empty)           | List of [HTTP Rules](#http-rule) for handling LCD requests.                                                         |
| **gRPC Settings**                     |                   |                                                                                                                     |
| `grpc.default`                        | `deny`            | Default action for gRPC requests.                                                                                   |
| `grpc.rules`                          | (Empty)           | List of [gRPC Rules](#grpc-rule) for handling gRPC requests.                                                        |
| **RPC Settings**                      |                   |                                                                                                                     |
| `rpc.default`                         | `deny`            | Default action for RPC requests.                                                                                    |
| `rpc.rules`                           | (Empty)           | List of [HTTP Rules](#http-rule) for handling RPC requests.                                                         |
| **JSON-RPC Settings**                 |                   |                                                                                                                     |
| `rpc.jsonrpc.default`                 | `deny`            | Default action for JSON-RPC requests.                                                                               |
| `rpc.jsonrpc.webSocketEnabled`        | `true`            | Enable or disable WebSocket support.                                                                                |
| `rpc.jsonrpc.webSocketConnections`    | `10`              | Maximum number of WebSocket connections.                                                                            |
| `rpc.jsonrpc.rules`                   | (Empty)           | List of [JSON-RPC Rules](#json-rpc-rule) for handling JSON-RPC requests.                                            |
| **EVM Settings**                      |                   |                                                                                                                     |
| `evm.rpc.default`                     | `deny`            | Default action for EVM RPC requests.                                                                                |
| `evm.rpc.rules`                       | (Empty)           | List of [JSON-RPC Rules](#json-rpc-rule) for handling EVM RPC requests.                                             |
| `evm.ws.default`                      | `deny`            | Default action for EVM WebSocket requests.                                                                          |
| `evm.ws.webSocketConnections`         | `10`              | Maximum number of WebSocket connections.                                                                            |
| `evm.ws.rules`                        | (Empty)           | List of [JSON-RPC Rules](#json-rpc-rule) for handling EVM WebSocket requests.                                       |

## Rules

### HTTP rule

| Option         | Default Value | Description                                                                                                          |
|----------------|---------------|----------------------------------------------------------------------------------------------------------------------|
| `priority`     | `1000`        | The priority of the rule. Lower numbers indicate higher priority.                                                    |
| `action`       | -             | The action to be taken for matching requests: either `allow` or `deny`.                                              |
| `paths`        | (Empty)       | List of paths this rule applies to. An empty list means all paths are included. **Wildcards supported**.             |
| `methods`      | (Empty)       | List of HTTP methods (e.g., `GET`, `POST`) this rule applies to. An empty list means all methods are included.       |
| `cache`        |               | Caching settings for the rule.                                                                                       |
| `cache.enable` | `false`       | Enable or disable caching for this rule.                                                                             |
| `cache.ttl`    | `5s`          | Time-to-live for the cache, which can override the global cache TTL. Default is the global setting if not specified. |

#### Example

```yaml
rules:
  - priority: 9999
    action: deny
    paths: [ ]
    methods: [ ]

  - priority: 1000
    action: allow
    paths:
      - /status
      - /block/**
    methods:
      - GET
    cache:
      enable: true
      ttl: 20s
```

### JSON-RPC rule

| Option         | Default Value | Description                                                                                                                                                                  |
|----------------|---------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `priority`     | `1000`        | The priority of the rule. Lower numbers indicate higher priority.                                                                                                            |
| `action`       | `allow`       | The action to be taken for matching requests: either `allow` or `deny`.                                                                                                      |
| `methods`      | (Empty)       | List of JSON-RPC methods this rule applies to. An empty list means all methods are included. **Wildcards supported**.                                                        |
| `params`       | (Empty)       | Dictionary of parameters to match specific JSON-RPC requests. Empty or missing means all parameters are included. **Wildcards supported (on params values only, not keys)**. |
| `cache`        |               | Caching settings for the rule.                                                                                                                                               |
| `cache.enable` | `false`       | Enable or disable caching for this rule.                                                                                                                                     |
| `cache.ttl`    | `5s`          | Time-to-live for the cache, which can override the global cache TTL. Default is the global setting if not specified.                                                         |

#### Example

```yaml
rules:
  - priority: 9999
    action: deny
    methods: [ ]
    params: { }

  - action: allow
    methods: [ "subscribe", "unsubscribe", "unsubscribe_all" ]

  - action: allow
    methods: [ "status" ]
    cache:
      enable: true

  - action: allow
    methods: [ "abci_query" ]
    params:
      path: /cosmos.bank.v1beta1.Query/AllBalances
    cache:
      enable: true
      ttl: 2s

  - action: allow
    methods: [ "abci_query" ]
    params:
      path: /cosmos.distribution.v1beta1.Query/**
```

### gRPC rule

| Option     | Default Value | Description                                                                                                       |
|------------|---------------|-------------------------------------------------------------------------------------------------------------------|
| `priority` | `1000`        | The priority of the rule. Lower numbers indicate higher priority.                                                 |
| `action`   | `allow`       | The action to be taken for matching requests: either `allow` or `deny`.                                           |
| `methods`  | (Empty)       | List of gRPC methods this rule applies to. An empty list means all methods are included. **Wildcards supported**. |

#### Example

```yaml
rules:
  - priority: 9999
    action: deny
    methods: [ ]

  - action: allow
    methods:
      - /cosmos.bank.v1beta1.Query/AllBalances

  - action: allow
    methods:
      - /cosmos.staking.v1beta1.Query/**
```