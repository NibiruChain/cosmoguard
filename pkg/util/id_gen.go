package util

import (
	"math"
	"math/rand"
	"strconv"
	"sync"
)

type UniqueID struct {
	generated sync.Map
}

func (u *UniqueID) ID() string {
	for {
		id := strconv.Itoa(rand.Intn(math.MaxInt32))
		v, ok := u.generated.Load(id)
		if !ok || !v.(bool) {
			u.generated.Store(id, true)
			return id
		}
	}
}

func (u *UniqueID) Release(id string) {
	u.generated.Store(id, false)
}
