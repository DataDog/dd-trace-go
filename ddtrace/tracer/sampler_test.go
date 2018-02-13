package tracer

import (
	"testing"

	"github.com/DataDog/dd-trace-go/ddtrace/internal"

	"github.com/stretchr/testify/assert"
)

func TestRateSampler(t *testing.T) {
	assert := assert.New(t)
	assert.True(NewRateSampler(1).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(1).Sample(internal.NoopSpan{}))
}

func TestRateSamplerFinishedSpan(t *testing.T) {
	rs := NewRateSampler(0.9999)
	tracer := newTracer(WithSampler(rs)) // high probability of sampling
	span := newBasicSpan("test")
	span.finished = true
	tracer.sample(span)
	if !rs.Sample(span) {
		t.Skip("wasn't sampled") // no flaky tests
	}
	_, ok := span.Metrics[sampleRateMetricKey]
	assert.False(t, ok)
}

func TestRateSamplerSetting(t *testing.T) {
	assert := assert.New(t)
	rs := NewRateSampler(1)
	assert.Equal(float64(1), rs.Rate())
	rs.SetRate(0.5)
	assert.Equal(float64(0.5), rs.Rate())
}
