package firewall

type SharedOptions struct {
	CacheConfig *CacheGlobalConfig
}

func DefaultSharedOptions() *SharedOptions {
	return &SharedOptions{}
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
}

func DefaultGrpcProxyOptions() *GrpcProxyOptions {
	cfg := &GrpcProxyOptions{}
	return cfg
}

type JsonRpcHandlerOptions struct {
	*SharedOptions
	WebsocketPath string
}

func DefaultJsonRpcHandlerOptions() *JsonRpcHandlerOptions {
	cfg := &JsonRpcHandlerOptions{
		WebsocketPath: "/websocket",
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
