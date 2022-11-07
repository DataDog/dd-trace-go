package fastdelta

import (
	"fmt"

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
