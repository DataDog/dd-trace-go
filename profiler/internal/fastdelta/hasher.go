package fastdelta

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/spaolacci/murmur3"
)

type Hasher struct {
	alg murmur3.Hash128
	st  *stringTable
	lx  *locationIndex

	scratch       [8]byte
	scratchHashes byHash
	scratchHash   Hash
}

func (h *Hasher) Sample(s *Sample) (Hash, error) {
	h.scratchHashes = h.scratchHashes[:0]
	for _, l := range s.Label {
		h.alg.Reset()
		h.alg.Write(h.st.h[l.Key][:])
		h.alg.Write(h.st.h[l.NumUnit][:])
		binary.BigEndian.PutUint64(h.scratch[:], uint64(l.Num))
		h.alg.Write(h.scratch[0:8])
		// TODO: do we need an if here?
		if uint64(l.Str) < uint64(len(h.st.h)) {
			h.alg.Write(h.st.h[l.Str][:])
		}
		h.alg.Sum(h.scratchHash[:0])
		h.scratchHashes = append(h.scratchHashes, h.scratchHash)
	}

	h.alg.Reset()
	for _, id := range s.LocationID {
		addr, ok := h.lx.Get(id)
		if !ok {
			return h.scratchHash, fmt.Errorf("invalid location index")
		}
		binary.LittleEndian.PutUint64(h.scratch[:], addr)
		h.alg.Write(h.scratch[:8])
	}

	// Memory profiles current have exactly one label ("bytes"), so there is no
	// need to sort. This saves ~0.5% of CPU time in our benchmarks.
	if len(h.scratchHashes) > 1 {
		sort.Sort(&h.scratchHashes) // passing &dc.hashes vs dc.hashes avoids an alloc here
	}

	for _, sub := range h.scratchHashes {
		copy(h.scratchHash[:], sub[:]) // avoid sub escape to heap
		h.alg.Write(h.scratchHash[:])
	}
	h.alg.Sum(h.scratchHash[:0])
	return h.scratchHash, nil
}
