package cosmoguard

type SharedOptions struct {
	CacheConfig    *CacheGlobalConfig
	MetricsEnabled bool
}

func DefaultSharedOptions() *SharedOptions {
	return &SharedOptions{
		MetricsEnabled: true,
	}
}

type HttpProxyOptions struct {
	*SharedOptions
	EndpointHandlers []*httpProxyEndpointHandler
}

func DefaultHttpProxyOptions() *HttpProxyOptions {
	cfg := &HttpProxyOptions{
		EndpointHandlers: make([]*httpProxyEndpointHandler, 0),
	}
	cfg.SharedOptions = DefaultSharedOptions()
	return cfg
}

type GrpcProxyOptions struct {
	*SharedOptions
}

func DefaultGrpcProxyOptions() *GrpcProxyOptions {
	cfg := &GrpcProxyOptions{}
	cfg.SharedOptions = DefaultSharedOptions()
	return cfg
}

type JsonRpcHandlerOptions struct {
	*SharedOptions
	WebsocketBackend     string
	WebsocketEnabled     bool
	WebsocketConnections int
	WebsocketPath        string
	UpstreamConstructor  UpstreamConnManagerConstructor
}

func DefaultJsonRpcHandlerOptions() *JsonRpcHandlerOptions {
	cfg := &JsonRpcHandlerOptions{
		WebsocketBackend:     "localhost:26657",
		WebsocketEnabled:     true,
		WebsocketConnections: 10,
		WebsocketPath:        defaultWebsocketPath,
		UpstreamConstructor:  CosmosUpstreamConnManager,
	}
	cfg.SharedOptions = DefaultSharedOptions()
	return cfg
}

type Option[T any] func(*T)

func WithCacheConfig[T SharedOptions | HttpProxyOptions | JsonRpcHandlerOptions](c *CacheGlobalConfig) Option[T] {
	return func(opts *T) {
		switch x := any(opts).(type) {
		case *SharedOptions:
			x.CacheConfig = c
		case *HttpProxyOptions:
			x.CacheConfig = c
		case *JsonRpcHandlerOptions:
			x.CacheConfig = c
		default:
			panic("unexpected use")
		}
	}
}

func WithMetricsEnabled[T SharedOptions | HttpProxyOptions | JsonRpcHandlerOptions | GrpcProxyOptions](b bool) Option[T] {
	return func(opts *T) {
		switch x := any(opts).(type) {
		case *SharedOptions:
			x.MetricsEnabled = b
		case *HttpProxyOptions:
			x.MetricsEnabled = b
		case *JsonRpcHandlerOptions:
			x.MetricsEnabled = b
		case *GrpcProxyOptions:
			x.MetricsEnabled = b
		default:
			panic("unexpected use")
		}
	}
}

func WithEndpointHandler[T HttpProxyOptions](endpoints []Endpoint, handler EndpointHandler) Option[T] {
	return func(opts *T) {
		switch x := any(opts).(type) {
		case *HttpProxyOptions:
			x.EndpointHandlers = append(x.EndpointHandlers, &httpProxyEndpointHandler{
				Endpoints: endpoints,
				Handler:   handler,
			})
		default:
			panic("unexpected use")
		}
	}
}

func WithWebSocketBackend[T JsonRpcHandlerOptions](backend string) Option[T] {
	return func(opts *T) {
		switch x := any(opts).(type) {
		case *JsonRpcHandlerOptions:
			x.WebsocketBackend = backend
		default:
			panic("unexpected use")
		}
	}
}

func WithWebSocketEnabled[T JsonRpcHandlerOptions](enabled bool) Option[T] {
	return func(opts *T) {
		switch x := any(opts).(type) {
		case *JsonRpcHandlerOptions:
			x.WebsocketEnabled = enabled
		default:
			panic("unexpected use")
		}
	}
}

func WithWebSocketConnections[T JsonRpcHandlerOptions](v int) Option[T] {
	return func(opts *T) {
		switch x := any(opts).(type) {
		case *JsonRpcHandlerOptions:
			x.WebsocketConnections = v
		default:
			panic("unexpected use")
		}
	}
}

func WithWebSocketPath[T JsonRpcHandlerOptions](path string) Option[T] {
	return func(opts *T) {
		switch x := any(opts).(type) {
		case *JsonRpcHandlerOptions:
			x.WebsocketPath = path
		default:
			panic("unexpected use")
		}
	}
}

func WithUpstreamManager[T JsonRpcHandlerOptions](constructor UpstreamConnManagerConstructor) Option[T] {
	return func(opts *T) {
		switch x := any(opts).(type) {
		case *JsonRpcHandlerOptions:
			x.UpstreamConstructor = constructor
		default:
			panic("unexpected use")
		}
	}
}
