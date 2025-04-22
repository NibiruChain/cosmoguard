package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache[K comparable, V any] struct {
	client    *redis.Client
	cfg       *Options
	namespace string
}

func NewRedisCache[K comparable, V any](connectionString string, namespace string, opts ...Option) (Cache[K, V], error) {
	options := defaultOptions()
	for _, opt := range opts {
		opt(options)
	}

	opt, err := redis.ParseURL(connectionString)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opt)
	return RedisCache[K, V]{
		client:    client,
		cfg:       options,
		namespace: namespace,
	}, nil
}

func (c RedisCache[K, V]) Set(ctx context.Context, key K, value V, ttl time.Duration) error {
	itemTTL := ttl
	if ttl == 0 {
		itemTTL = c.cfg.TTL
	}
	v, err := EncodeValue(value)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, c.getKey(key), v, itemTTL).Err()
}

func (c RedisCache[K, V]) Get(ctx context.Context, key K) (V, error) {
	v, err := c.client.Get(ctx, c.getKey(key)).Bytes()
	if err != nil {
		var result V
		if errors.Is(err, redis.Nil) {
			return result, ErrNotFound
		}
		return result, err
	}
	return DecodeValue[V](v)
}

func (c RedisCache[K, V]) Has(ctx context.Context, key K) (bool, error) {
	n, err := c.client.Exists(ctx, c.getKey(key)).Result()
	return n > 0, err
}

func (c RedisCache[K, V]) getKey(key K) string {
	return fmt.Sprintf("%s:%v", c.namespace, key)
}
