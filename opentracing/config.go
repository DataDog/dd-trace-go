package opentracing

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	datadog "github.com/DataDog/dd-trace-go/tracer"
	ot "github.com/opentracing/opentracing-go"
)

// Configuration struct to configure a Datadog Tracer
type Configuration struct {
	Enabled       bool    // when disabled, a no-op implementation is returned
	Debug         bool    // when enabled, more details are written in logs
	ServiceName   string  // define the service name for this application
	SampleRate    float64 // set the Tracer sample rate [0, 1]
	AgentHostname string  // change the hostname where traces are sent
	AgentPort     string  // change the port where traces are sent
}

// NewConfiguration creates a `Configuration` object with default values.
func NewConfiguration() *Configuration {
	// default service name is the Go binary name
	binaryName := filepath.Base(os.Args[0])

	// Configuration struct with default values
	return &Configuration{
		Enabled:       true,
		Debug:         false,
		ServiceName:   binaryName,
		SampleRate:    1,
		AgentHostname: "localhost",
		AgentPort:     "8126",
	}
}

type noopCloser struct{}

func (c *noopCloser) Close() error { return nil }

// NewDatadogTracer uses a Configuration object to initialize a
// Datadog Tracer. The initialization returns a `io.Closer` that
// can be used to graceful shutdown the tracer. If the configuration object
// defines a disabled Tracer, a no-op implementation is returned.
func NewDatadogTracer(config *Configuration) (ot.Tracer, io.Closer, error) {
	if config.ServiceName == "" {
		// abort initialization if a `ServiceName` is not defined
		return nil, nil, errors.New("A Datadog Tracer requires a valid `ServiceName` set")
	}

	if config.Enabled == false {
		// return a no-op implementation so Datadog provides the minimum overhead
		return &ot.NoopTracer{}, &noopCloser{}, nil
	}

	// configure a Datadog Tracer
	transport := datadog.NewTransport(config.AgentHostname, config.AgentPort)
	tracer := &Tracer{impl: datadog.NewTracerTransport(transport)}
	tracer.impl.SetDebugLogging(config.Debug)
	tracer.impl.SetSampleRate(config.SampleRate)

	return tracer, tracer, nil
}
