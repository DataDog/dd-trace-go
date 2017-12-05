package opentracing_test

import (
	// datadog namespace is suggested
	datadog "github.com/DataDog/dd-trace-go/opentracing"
	opentracing "github.com/opentracing/opentracing-go"
)

func Example_initialization() {
	// create a Tracer configuration
	config := datadog.NewConfiguration()
	config.ServiceName = "api-intake"
	config.AgentHostname = "ddagent.consul.local"

	// initialize a Tracer and ensure a graceful shutdown
	// using the `closer.Close()`
	tracer, closer, err := datadog.NewTracer(config)
	if err != nil {
		// handle the configuration error
	}
	defer closer.Close()

	// set the Datadog tracer as a GlobalTracer
	opentracing.SetGlobalTracer(tracer)
	startWebServer()
}

func startWebServer() {
	// start a web server
}
