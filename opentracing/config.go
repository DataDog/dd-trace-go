package opentracing

import (
	"os"
	"path/filepath"
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
