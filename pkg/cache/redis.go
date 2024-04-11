package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache[K comparable, V any] struct {
	client *redis.Client
	cfg    *Options
}

func NewRedisCache[K comparable, V any](connectionString string, opts ...Option) (Cache[K, V], error) {
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
		client: client,
		cfg:    options,
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
	return c.client.Set(ctx, getRedisKey(key), v, itemTTL).Err()
}

func (c RedisCache[K, V]) Get(ctx context.Context, key K) (V, error) {
	v, err := c.client.Get(ctx, getRedisKey(key)).Bytes()
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
	n, err := c.client.Exists(ctx, getRedisKey(key)).Result()
	return n > 0, err
}

func getRedisKey[K comparable](key K) string {
	return fmt.Sprintf("%v", key)
}
