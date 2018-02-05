package tracer

import (
	"math"
	"sync"
)

const (
	// sampleRateMetricKey is the metric key holding the applied sample rate. Has to be the same as the Agent.
	sampleRateMetricKey = "_sample_rate"

	// constants used for the Knuth hashing, same as agent.
	knuthFactor = uint64(1111111111111111111)
)

// Sampler is the generic interface of any sampler. Must be safe for concurrent use.
type Sampler interface {
	Sample(span *Span) // Tells if a trace is sampled and sets `span.Sampled`
}

// RateSampler samples from a sample rate.
type RateSampler struct {
	sync.RWMutex
	rate float64
}

func NewAllSampler() *RateSampler { return NewRateSampler(1) }

// NewRateSampler returns an initialized RateSampler with its sample rate.
func NewRateSampler(rate float64) *RateSampler {
	return &RateSampler{rate: rate}
}

func (s *RateSampler) Rate() float64 {
	s.RLock()
	defer s.RUnlock()
	return s.rate
}

func (s *RateSampler) SetRate(rate float64) {
	s.Lock()
	s.rate = rate
	s.Unlock()
}

// Sample samples a span
func (s *RateSampler) Sample(span *Span) {
	s.RLock()
	defer s.RUnlock()

	if s.rate < 1 {
		span.Sampled = span.TraceID*knuthFactor < uint64(s.rate*math.MaxUint64)
		span.SetMetric(sampleRateMetricKey, s.rate)
	}
}
