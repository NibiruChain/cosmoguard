package cache

import "time"

const (
	defaultCacheTTL = 5 * time.Second
)

func defaultOptions() *Options {
	return &Options{
		TTL: defaultCacheTTL,
	}
}

type Options struct {
	TTL time.Duration
}

type Option func(*Options)

func DefaultTTL(ttl time.Duration) Option {
	return func(o *Options) {
		o.TTL = ttl
	}
}
