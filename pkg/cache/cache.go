package cache

import (
	"context"
	"errors"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

const (
	DefaultNamespace = "default"
)

var (
	ErrNotFound = errors.New("item not found in cache")
)

type Cache[K comparable, V any] interface {
	Set(ctx context.Context, key K, value V, ttl time.Duration) error
	Get(ctx context.Context, key K) (V, error)
	Has(ctx context.Context, key K) (bool, error)
}

func EncodeValue(value any) ([]byte, error) {
	return msgpack.Marshal(value)
}

func DecodeValue[V any](data []byte) (V, error) {
	var result V
	err := msgpack.Unmarshal(data, &result)
	return result, err
}
