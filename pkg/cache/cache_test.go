package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type testCase[K comparable, V any] struct {
	Key      K
	Value    V
	TTL      time.Duration
	Wait     time.Duration
	Expected testCaseExpect[V]
}

type testCaseExpect[V any] struct {
	Value V
	Err   error
}

func testEncodeDecodeAny[T any](t *testing.T, testItems []T) {
	for _, item := range testItems {
		encoded, err := EncodeValue(item)
		assert.NoError(t, err)

		decoded, err := DecodeValue[T](encoded)
		assert.NoError(t, err)
		assert.Equal(t, item, decoded)
	}
}

func TestEncodeDecodeInt(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	testEncodeDecodeAny(t, items)
}

func TestEncodeDecodeString(t *testing.T) {
	items := []string{"hello world", ""}
	testEncodeDecodeAny(t, items)
}

func TestEncodeDecodeBool(t *testing.T) {
	items := []bool{true, false}
	testEncodeDecodeAny(t, items)
}

type testStruct struct {
	I int
	S string
}

func TestEncodeDecodeStruct(t *testing.T) {
	items := []testStruct{
		{
			I: 1,
			S: "hello world",
		},
		{
			I: 5,
			S: "",
		},
	}
	testEncodeDecodeAny(t, items)
}
