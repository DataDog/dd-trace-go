package opentracing

import (
	"testing"

	ot "github.com/opentracing/opentracing-go"
	"github.com/stretchr/testify/assert"
)

func TestConfigurationDefaults(t *testing.T) {
	assert := assert.New(t)

	config := NewConfiguration()
	assert.Equal(true, config.Enabled)
	assert.Equal(false, config.Debug)
	assert.Equal("opentracing.test", config.ServiceName)
	assert.Equal("localhost", config.AgentHostname)
	assert.Equal("8126", config.AgentPort)
}

func TestTracerConstructor(t *testing.T) {
	assert := assert.New(t)

	config := NewConfiguration()
	config.ServiceName = ""
	tracer, closer, err := NewDatadogTracer(config)
	assert.Nil(tracer)
	assert.Nil(closer)
	assert.NotNil(err)
	assert.Equal("A Datadog Tracer requires a valid `ServiceName` set", err.Error())
}

func TestDisabledTracer(t *testing.T) {
	assert := assert.New(t)

	config := NewConfiguration()
	config.Enabled = false
	tracer, closer, err := NewDatadogTracer(config)
	assert.IsType(&ot.NoopTracer{}, tracer)
	assert.IsType(&noopCloser{}, closer)
	assert.Nil(err)
}

func TestConfiguration(t *testing.T) {
	assert := assert.New(t)

	config := NewConfiguration()
	config.SampleRate = 0
	config.AgentHostname = "ddagent.consul.local"
	config.AgentPort = "58126"
	tracer, closer, err := NewDatadogTracer(config)
	assert.NotNil(tracer)
	assert.NotNil(closer)
	assert.Nil(err)
}
