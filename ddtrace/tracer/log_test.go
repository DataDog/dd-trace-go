package tracer

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStartupLog(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(testLogger)
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
		defer stop()

		logStartup(tracer)
		assert.Len(tp.Lines(), 1)
		assert.Regexp(`Datadog Tracer v1\.25\.0 INFO: Startup: {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test","agent_url":"localhost:8126","agent_error":{.*},"debug":false,"analytics_enabled":false,"sample_rate":"NaN","sampling_rules":\[\],"sampling_rules_error":null,"tags":null,"runtime_metrics_enabled":false,"health_metrics_enabled":false,"dd_version":"","architecture":"[^"]*","global_service":""}`, tp.Lines()[0])
	})

	t.Run("configured", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(testLogger)
		os.Setenv("DD_TRACE_SAMPLE_RATE", "0.123")
		defer os.Unsetenv("DD_TRACE_SAMPLE_RATE")
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithService("foo.bar"))
		defer stop()

		logStartup(tracer)
		assert.Regexp(`Datadog Tracer v1\.25\.0 INFO: Startup: {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"foo\.bar","agent_url":"localhost:8126","agent_error":{.*},"debug":false,"analytics_enabled":false,"sample_rate":"0\.123000","sampling_rules":\[\],"sampling_rules_error":null,"tags":null,"runtime_metrics_enabled":false,"health_metrics_enabled":false,"dd_version":"","architecture":"[^"]*","global_service":"foo\.bar"}`, tp.Lines()[0])
	})
}
