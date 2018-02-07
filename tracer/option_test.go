package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func withTransport(t transport) Option {
	return func(c *config) {
		c.transport = t
	}
}

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
	)
	c := tracer.config
	assert.Equal(float64(0.5), c.sampler.(RateSampler).Rate())
	assert.Equal("api-intake", c.serviceName)
	assert.Equal("ddagent.consul.local:58126", c.agentAddr)
	assert.NotNil(c.globalTags)
	assert.Equal("v", c.globalTags["k"])
}

func TestTracerOptionsWithGlobalTags(t *testing.T) {
	checkPanicked := func(yes bool) {
		if didPanic := recover() != nil; didPanic != yes {
			t.Errorf("panic expectancy: %t", yes)
		}
	}
	t.Run("uneven", func(t *testing.T) {
		defer checkPanicked(true)
		WithGlobalTags("a", "b", "c")
	})
	t.Run("one-argument", func(t *testing.T) {
		defer checkPanicked(true)
		WithGlobalTags("a")
	})
	t.Run("no-argument", func(t *testing.T) {
		defer checkPanicked(true)
		WithGlobalTags()
	})
	t.Run("just-two", func(t *testing.T) {
		defer checkPanicked(false)
		var c config
		WithGlobalTags("k", "v")(&c)
		assert.New(t).Equal("v", c.globalTags["k"])
	})
	t.Run("not-string-key", func(t *testing.T) {
		defer checkPanicked(true)
		WithGlobalTags(1, "a")(new(config))
	})
	t.Run("good", func(t *testing.T) {
		defer checkPanicked(false)
		var c config
		WithGlobalTags("a", "b", "c", 4)(&c)
		assert := assert.New(t)
		assert.Equal("b", c.globalTags["a"])
		assert.Equal(4, c.globalTags["c"])
	})
}
