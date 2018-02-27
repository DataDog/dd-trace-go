package tracer

import (
	"math"
	"sync"

	"github.com/DataDog/dd-trace-go/ddtrace"
)

// Sampler is the generic interface of any sampler. Must be safe for concurrent use.
type Sampler interface {
	// Sample should return true if the given span should be sampled.
	Sample(span Span) bool
}

// RateSampler is a sampler implementation which allows setting and getting a sample rate.
// A RateSampler implementation is expected to be safe for concurrent use.
type RateSampler interface {
	Sampler

	// Rate should return the current sample rate of the sampler.
	Rate() float64

	// SetRate should set a new sample rate for the RateSampler.
	SetRate(rate float64)
}

// rateSampler samples from a sample rate.
type rateSampler struct {
	sync.RWMutex
	rate float64
}

// NewAllSampler is simply a short-hand for NewRateSampler(1).
func NewAllSampler() RateSampler { return NewRateSampler(1) }

// NewRateSampler returns an initialized RateSampler with its sample rate.
func NewRateSampler(rate float64) RateSampler {
	return &rateSampler{rate: rate}
}

// Rate returns the current rate of the sampler.
func (r *rateSampler) Rate() float64 {
	r.RLock()
	defer r.RUnlock()
	return r.rate
}

// SetRate sets a new sampling rate.
func (r *rateSampler) SetRate(rate float64) {
	r.Lock()
	r.rate = rate
	r.Unlock()
}

// constants used for the Knuth hashing, same as agent.
const knuthFactor = uint64(1111111111111111111)

// Sample returns true if the given span should be sampled.
func (r *rateSampler) Sample(spn ddtrace.Span) bool {
	s, ok := spn.(*span)
	if !ok {
		return false
	}
	r.RLock()
	defer r.RUnlock()
	if r.rate < 1 {
		return s.TraceID*knuthFactor < uint64(r.rate*math.MaxUint64)
	}
	return true
}
