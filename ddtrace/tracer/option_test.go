package tracer

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

func withTransport(t transport) StartOption {
	return func(c *config) {
		c.transport = t
	}
}

func TestTracerOptionsDefaults(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		assert := assert.New(t)
		var c config
		defaults(&c)
		assert.Equal(float64(1), c.sampler.(RateSampler).Rate())
		assert.Equal("tracer.test", c.serviceName)
		assert.Equal("localhost:8126", c.agentAddr)
		assert.Equal(nil, c.httpRoundTripper)
	})

	t.Run("analytics", func(t *testing.T) {
		assert := assert.New(t)
		assert.True(math.IsNaN(globalconfig.AnalyticsRate()))
		newTracer(WithAnalyticsRate(0.5))
		assert.Equal(0.5, globalconfig.AnalyticsRate())
		newTracer(WithAnalytics(false))
		assert.True(math.IsNaN(globalconfig.AnalyticsRate()))
		newTracer(WithAnalytics(true))
		assert.Equal(1., globalconfig.AnalyticsRate())
	})

	t.Run("other", func(t *testing.T) {
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
	})
}
