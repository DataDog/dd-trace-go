package datastreams

import (
	"hash/fnv"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	schemaSampleIntervalNs = int64(time.Second * 30)
)

var (
	// globalSchemaSampler stores the current schema sampler
	globalSchemaSampler *schemaSampler
)

func init() {
	globalSchemaSampler = &schemaSampler{}
}

type schemaSampler struct {
	weight           atomic.Int64
	lastSampleMillis atomic.Int64
}

func SampleSchema() (weight int64) {
	return globalSchemaSampler.sampleSchema(time.Now().UnixNano())
}

func (s *schemaSampler) sampleSchema(currentTimeNs int64) (weight int64) {
	s.weight.Add(1)
	lastSample := s.lastSampleMillis.Load()
	if currentTimeNs >= lastSample+schemaSampleIntervalNs {
		if s.lastSampleMillis.CompareAndSwap(lastSample, currentTimeNs) {
			return s.weight.Swap(0)
		}
	}
	return 0
}

func GetSchemaID(schema string) string {
	h := fnv.New64()
	h.Write([]byte(schema))
	return strconv.FormatUint(h.Sum64(), 10)
}
