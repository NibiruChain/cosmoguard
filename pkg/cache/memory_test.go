package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func testMemoryCache[K comparable, V any](t *testing.T, testItems []testCase[K, V]) {
	cache, _ := NewMemoryCache[K, V](DefaultNamespace)
	for _, tc := range testItems {
		err := cache.Set(context.TODO(), tc.Key, tc.Value, tc.TTL)
		assert.NoError(t, err)
		if tc.Wait > 0 {
			time.Sleep(tc.Wait)
		}
		v, err := cache.Get(context.TODO(), tc.Key)
		assert.ErrorIs(t, err, tc.Expected.Err)
		assert.Equal(t, tc.Expected.Value, v)
	}
}

func TestMemoryCacheStringInt(t *testing.T) {
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
	testMemoryCache(t, items)
}

func TestMemoryCacheStringString(t *testing.T) {
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
	testMemoryCache(t, items)
}

func TestMemoryCacheStringStruct(t *testing.T) {
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
	testMemoryCache(t, items)
}
