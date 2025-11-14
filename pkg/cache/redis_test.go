package cache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
)

func testRedisCache[K comparable, V any](t *testing.T, testItems []testCase[K, V]) {
	s := miniredis.RunT(t)
	connectionString := fmt.Sprintf("redis://@%s", s.Addr())
	cache, _ := NewRedisCache[K, V](&connectionString, nil, DefaultNamespace)
	for _, tc := range testItems {
		err := cache.Set(context.Background(), tc.Key, tc.Value, tc.TTL)
		assert.NoError(t, err)

		_, err = s.Get(fmt.Sprintf("%s:%v", DefaultNamespace, tc.Key))
		assert.NoError(t, err)

		if tc.TTL > 0 {
			s.SetTTL(fmt.Sprintf("%s:%v", DefaultNamespace, tc.Key), tc.TTL)
		} else {
			s.SetTTL(fmt.Sprintf("%s:%v", DefaultNamespace, tc.Key), defaultCacheTTL)
		}

		if tc.Wait > 0 {
			s.FastForward(tc.Wait)
		}

		v, err := cache.Get(context.Background(), tc.Key)
		assert.ErrorIs(t, err, tc.Expected.Err)
		assert.Equal(t, tc.Expected.Value, v)
	}
}

func TestRedisCacheStringInt(t *testing.T) {
	items := []testCase[string, int]{
		{
			Key:   "A",
			Value: 1,
			TTL:   100 * time.Millisecond,
			Wait:  50 * time.Millisecond,
			Expected: testCaseExpect[int]{
				Value: 1,
				Err:   nil,
			},
		},
		{
			Key:   "B",
			Value: 1,
			TTL:   100 * time.Millisecond,
			Wait:  150 * time.Millisecond,
			Expected: testCaseExpect[int]{
				Value: 0,
				Err:   ErrNotFound,
			},
		},
	}
	testRedisCache(t, items)
}

func TestRedisCacheStringString(t *testing.T) {
	items := []testCase[string, string]{
		{
			Key:   "C",
			Value: "hello",
			TTL:   0,
			Expected: testCaseExpect[string]{
				Value: "hello",
				Err:   nil,
			},
		},
	}
	testRedisCache(t, items)
}

func TestRedisCacheStringStruct(t *testing.T) {
	items := []testCase[string, testStruct]{
		{
			Key: "C",
			Value: testStruct{
				S: "test",
				I: 5,
			},
			TTL: 0,
			Expected: testCaseExpect[testStruct]{
				Value: testStruct{
					S: "test",
					I: 5,
				},
				Err: nil,
			},
		},
	}
	testRedisCache(t, items)
}
