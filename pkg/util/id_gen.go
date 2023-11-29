package util

import (
	"math"
	"math/rand"
	"sync"
)

type UniqueID struct {
	generated sync.Map
}

func (u *UniqueID) ID() int {
	for {
		i := rand.Intn(math.MaxInt32)
		v, ok := u.generated.Load(i)
		if !ok || !v.(bool) {
			u.generated.Store(i, true)
			return i
		}
	}
}

func (u *UniqueID) Release(id int) {
	u.generated.Store(id, false)
}
