package fastdelta

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/spaolacci/murmur3"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/pproflite"
)

// As of Go 1.19, the Go heap profile has 4 values per sample. This is the most
// for any of the Go runtime profiles. In order to make the map of samples to
// their values more GC-friendly, we prefer to have the values for that mapping
// be fixed-size arrays rather than slices. However, this means we can't process
// profiles with more than this many values per sample.
const maxSampleValues = 4

type sampleValue [maxSampleValues]int64

func NewDeltaMap(st *stringTable, lx *locationIndex, fields []valueType) *DeltaMap {
	return &DeltaMap{
		h:                    Hasher{alg: murmur3.New128(), st: st, lx: lx},
		m:                    map[Hash]sampleValue{},
		st:                   st,
		fields:               fields,
		computeDeltaForValue: make([]bool, 0, 4),
	}
}

type DeltaMap struct {
	h  Hasher
	m  map[Hash]sampleValue
	st *stringTable
	// fields are the name and types of the values in a sample for which we should
	// compute the difference.
	fields               []valueType
	computeDeltaForValue []bool
	// valueTypeIndices are string table indices of the sample value type
	// names (e.g.  "alloc_space", "cycles"...) and their types ("count", "bytes")
	// included in DeltaComputer as the indices are written in indexPass
	// and read in mergeSamplesPass
	valueTypeIndices [][2]int
}

func (dm *DeltaMap) Reset() {
	dm.valueTypeIndices = dm.valueTypeIndices[:0]
	dm.computeDeltaForValue = dm.computeDeltaForValue[:0]
}

// TODO(fg) get rid of this?
func (dm *DeltaMap) AddSampleType(st *pproflite.SampleType) error {
	dm.valueTypeIndices = append(dm.valueTypeIndices, [2]int{int(st.Type), int(st.Unit)})
	if len(dm.valueTypeIndices) > maxSampleValues {
		return fmt.Errorf("profile has %d values per sample, exceeding the maximum %d", len(dm.valueTypeIndices), maxSampleValues)
	}
	return nil
}

// Delta updates sample.Value by looking up the previous values for this sample
// and substracting them from the current values. The returned boolean is true
// if the the new sample.Value contains at least one non-zero value.
func (dm *DeltaMap) Delta(sample *pproflite.Sample) (bool, error) {
	dm.prepare()

	hash, err := dm.h.Sample(sample)
	if err != nil {
		return false, err
	}

	// TODO(fg) get rid of this
	var val sampleValue
	copy(val[:], sample.Value)

	old, ok := dm.m[hash]
	dm.m[hash] = val // save for next time
	if ok {
		all0 := true
		for i := 0; i < len(dm.computeDeltaForValue); i++ {
			if dm.computeDeltaForValue[i] {
				val[i] = val[i] - old[i]
			}
			if val[i] != 0 {
				all0 = false
			}
		}
		copy(sample.Value, val[:])
		if all0 {
			// If the sample has all 0 values, we drop it
			// this matches the behavior of Google's pprof library
			// when merging profiles
			return false, nil
		}
	}
	return true, nil
}

func (dm *DeltaMap) prepare() {
	if len(dm.computeDeltaForValue) > 0 {
		return
	}
	for len(dm.computeDeltaForValue) < len(dm.valueTypeIndices) {
		dm.computeDeltaForValue = append(dm.computeDeltaForValue, false)
	}
	for _, field := range dm.fields {
		for i, vtIdxs := range dm.valueTypeIndices {
			typeMatch := dm.st.Equals(vtIdxs[0], field.Type)
			unitMatch := dm.st.Equals(vtIdxs[1], field.Unit)
			if typeMatch && unitMatch {
				dm.computeDeltaForValue[i] = true
				break
			}
		}
	}
}

// Hash is a 128-bit hash representing sample identity
type Hash [16]byte

type byHash []Hash

func (h byHash) Len() int           { return len(h) }
func (h byHash) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h byHash) Less(i, j int) bool { return bytes.Compare(h[i][:], h[j][:]) == -1 }

type Hasher struct {
	alg murmur3.Hash128
	st  *stringTable
	lx  *locationIndex

	scratch       [8]byte
	scratchHashes byHash
	scratchHash   Hash
}

func (h *Hasher) Sample(s *pproflite.Sample) (Hash, error) {
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
