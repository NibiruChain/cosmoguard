package cache

import (
	"context"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

type MemoryCache[K comparable, V any] struct {
	cache *ttlcache.Cache[K, V]
}

func NewMemoryCache[K comparable, V any](opts ...Option) (Cache[K, V], error) {
	options := defaultOptions()
	for _, opt := range opts {
		opt(options)
	}

	cacheOptions := []ttlcache.Option[K, V]{
		ttlcache.WithTTL[K, V](options.TTL),
	}
	c := MemoryCache[K, V]{
		cache: ttlcache.New[K, V](cacheOptions...),
	}
	go c.cache.Start()
	return c, nil
}

func (c MemoryCache[K, V]) Set(_ context.Context, key K, value V, ttl time.Duration) error {
	c.cache.Set(key, value, ttl)
	return nil
}

func (c MemoryCache[K, V]) Get(_ context.Context, key K) (V, error) {
	item := c.cache.Get(key)
	if item != nil {
		return item.Value(), nil
	}
	var result V
	return result, ErrNotFound
}

func (c MemoryCache[K, V]) Has(_ context.Context, key K) (bool, error) {
	return c.cache.Has(key), nil
}
