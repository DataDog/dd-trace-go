package tracer

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

	"github.com/stretchr/testify/assert"
)

func TestRateSampler(t *testing.T) {
	assert := assert.New(t)
	assert.True(NewRateSampler(1).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(1).Sample(internal.NoopSpan{}))
}

func TestRateSamplerSetting(t *testing.T) {
	assert := assert.New(t)
	rs := NewRateSampler(1)
	assert.Equal(float64(1), rs.Rate())
	rs.SetRate(0.5)
	assert.Equal(float64(0.5), rs.Rate())
}
