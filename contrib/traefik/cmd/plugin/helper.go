package plugin

import (
	"math"
	"math/rand"
)

func generateID() uint32 {
	return uint32(rand.Intn(math.MaxUint32))
}
