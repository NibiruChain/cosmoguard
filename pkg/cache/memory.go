package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

type namespacedKey[K comparable] struct {
	Namespace string
	Key       K
}

func (k namespacedKey[K]) String() string {
	return fmt.Sprintf("[%s]%v", k.Namespace, k.Key)
}

type MemoryCache[K comparable, V any] struct {
	Namespace string
	cache     *ttlcache.Cache[namespacedKey[K], V]
}

func NewMemoryCache[K comparable, V any](namespace string, opts ...Option) (Cache[K, V], error) {
	options := defaultOptions()
	for _, opt := range opts {
		opt(options)
	}

	cacheOptions := []ttlcache.Option[namespacedKey[K], V]{
		ttlcache.WithDisableTouchOnHit[namespacedKey[K], V](),
		ttlcache.WithTTL[namespacedKey[K], V](options.TTL),
	}
	c := MemoryCache[K, V]{
		Namespace: namespace,
		cache:     ttlcache.New[namespacedKey[K], V](cacheOptions...),
	}
	go c.cache.Start()
	return c, nil
}

func (c MemoryCache[K, V]) key(key K) namespacedKey[K] {
	return namespacedKey[K]{
		Namespace: c.Namespace,
		Key:       key,
	}
}

func (c MemoryCache[K, V]) Set(_ context.Context, key K, value V, ttl time.Duration) error {
	c.cache.Set(c.key(key), value, ttl)
	return nil
}

func (c MemoryCache[K, V]) Get(_ context.Context, key K) (V, error) {
	item := c.cache.Get(c.key(key))
	if item != nil {
		return item.Value(), nil
	}
	var result V
	return result, ErrNotFound
}

func (c MemoryCache[K, V]) Has(_ context.Context, key K) (bool, error) {
	return c.cache.Has(c.key(key)), nil
}
