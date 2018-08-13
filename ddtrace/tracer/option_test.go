package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTracerOptionsDefaults(t *testing.T) {
	assert := assert.New(t)
	var c config
	defaults(&c)
	assert.Equal(float64(1), c.sampler.(RateSampler).Rate())
	assert.Equal("tracer.test", c.serviceName)
	assert.Equal("localhost:8126", c.agentAddr)
}

func TestTracerOptions(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(
		WithSampler(NewRateSampler(0.5)),
		WithServiceName("api-intake"),
		WithAgentAddr("ddagent.consul.local:58126"),
		WithGlobalTag("k", "v"),
		WithDebugMode(true),
	)
	c := tracer.config
	assert.Equal(float64(0.5), c.sampler.(RateSampler).Rate())
	assert.Equal("api-intake", c.serviceName)
	assert.Equal("ddagent.consul.local:58126", c.agentAddr)
	assert.NotNil(c.globalTags)
	assert.Equal("v", c.globalTags["k"])
	assert.True(c.debug)
}
