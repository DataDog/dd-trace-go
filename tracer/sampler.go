package tracer

import (
	"math"
	"sync"

	opentracing "github.com/opentracing/opentracing-go"
)

// Sampler is the generic interface of any sampler. Must be safe for concurrent use.
type Sampler interface {
	// Sample should return true if the given span should be sampled.
	Sample(span opentracing.Span) bool
}

// RateSampler is a sampler implementation which allows setting and getting a sample rate.
// A RateSampler implementation is expected to be safe for concurrent use.
type RateSampler interface {
	Sampler

	// Rate returns the current sample rate of the sampler.
	Rate() float64

	// SetRate sets a new sample rate for the RateSampler.
	SetRate(rate float64)
}

// rateSampler samples from a sample rate.
type rateSampler struct {
	sync.RWMutex
	rate float64
}

func NewAllSampler() RateSampler { return NewRateSampler(1) }

// NewRateSampler returns an initialized RateSampler with its sample rate.
func NewRateSampler(rate float64) RateSampler {
	return &rateSampler{rate: rate}
}

func (s *rateSampler) Rate() float64 {
	s.RLock()
	defer s.RUnlock()
	return s.rate
}

func (s *rateSampler) SetRate(rate float64) {
	s.Lock()
	s.rate = rate
	s.Unlock()
}

// constants used for the Knuth hashing, same as agent.
const knuthFactor = uint64(1111111111111111111)

// Sample returns true if the given span should be sampled.
func (r *rateSampler) Sample(s opentracing.Span) bool {
	span, ok := s.(*span)
	if !ok {
		// should never happen, but if it does, unknown spans
		// would be useless for our agent.
		return false
	}
	r.RLock()
	defer r.RUnlock()
	if r.rate < 1 {
		return span.TraceID*knuthFactor < uint64(r.rate*math.MaxUint64)
	}
	return true
}
