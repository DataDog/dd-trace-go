package main

import (
	"context"
	"errors"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"math/rand"
	"time"
)

func fixedDistribution(env, service, version, name, spanType, resource, statusCode, hostname string, synthetics bool, hasError bool, latencies []point) {
	for _, l := range latencies {
		for i := 0; i < l.weight; i++ {
			start := time.Now()
			s, ctx := tracer.StartSpanFromContext(context.Background(),
				name,
				tracer.ServiceName(service),
				tracer.ResourceName(resource),
				tracer.SpanType(spanType),
				tracer.StartTime(start))
			if synthetics {
				s.SetTag("_dd.origin", "synthetics")
			}
			s.SetTag(ext.HTTPCode, statusCode)
			s.SetTag(ext.Version, version)
			s.SetTag(ext.Environment, env)
			if hostname != "" {
				s.SetTag("_dd.hostname", hostname)
			}
			child, _ := tracer.StartSpanFromContext(ctx, name, tracer.ServiceName("child"), tracer.StartTime(start))
			child.Finish(tracer.FinishTime(start.Add(l.latency)))
			var err error
			if hasError {
				err = errors.New("error_1")
			}
			s.Finish(tracer.WithError(err), tracer.FinishTime(start.Add(l.latency)))
		}
	}
}

type point struct {
	latency time.Duration
	weight int
}

var latencies1 = []point{{latency: time.Millisecond*900, weight: 50}, {latency: time.Millisecond*1000, weight: 25}, {latency: time.Millisecond*1100, weight: 20}, {latency: time.Millisecond*1500, weight: 4}, {latency: time.Millisecond*2000, weight: 1}}

var testCases = []func(){
	// no error
	func() { fixedDistribution("test_1", "service_1", "version_1", "name_1", "type_1", "resource_1", "100", "", false, false, latencies1) },
	func() { fixedDistribution("test_2", "service_1", "version_1", "name_1", "type_1", "resource_1", "100", "", false, false, latencies1) },
	func() { fixedDistribution("test_1", "service_2", "version_1", "name_1", "type_1", "resource_1", "100", "", false, false, latencies1) },
	func() { fixedDistribution("test_1", "service_1", "version_2", "name_1", "type_1", "resource_1", "100", "", false, false, latencies1) },
	func() { fixedDistribution("test_1", "service_1", "version_1", "name_2", "type_1", "resource_1", "100", "", false, false, latencies1) },
	func() { fixedDistribution("test_1", "service_1", "version_1", "name_1", "type_2", "resource_1", "100", "", false, false, latencies1) },
	func() { fixedDistribution("test_1", "service_1", "version_1", "name_1", "type_1", "resource_2", "100", "", false, false, latencies1) },
	func() { fixedDistribution("test_1", "service_1", "version_1", "name_1", "type_1", "resource_1", "200", "", false, false, latencies1) },
	func() { fixedDistribution("test_1", "service_1", "version_1", "name_1", "type_1", "resource_1", "100", "", true, false, latencies1) },

	// error
	func() { fixedDistribution("test_1", "service_1", "version_1", "name_1", "type_1", "resource_1", "100", "", false, true, latencies1) },
	func() { fixedDistribution("test_2", "service_1", "version_1", "name_1", "type_1", "resource_1", "100", "", false, true, latencies1) },
	func() { fixedDistribution("test_1", "service_2", "version_1", "name_1", "type_1", "resource_1", "100", "", false, true, latencies1) },
	func() { fixedDistribution("test_1", "service_1", "version_2", "name_1", "type_1", "resource_1", "100", "", false, true, latencies1) },
	func() { fixedDistribution("test_1", "service_1", "version_1", "name_2", "type_1", "resource_1", "100", "", false, true, latencies1) },
	func() { fixedDistribution("test_1", "service_1", "version_1", "name_1", "type_2", "resource_1", "100", "", false, true, latencies1) },
	func() { fixedDistribution("test_1", "service_1", "version_1", "name_1", "type_1", "resource_2", "100", "", false, true, latencies1) },
	func() { fixedDistribution("test_1", "service_1", "version_1", "name_1", "type_1", "resource_1", "200", "", false, true, latencies1) },
	func() { fixedDistribution("test_1", "service_1", "version_1", "name_1", "type_1", "resource_1", "100", "", true, true, latencies1) },
}

func main() {
	rand.Seed(987927)
	tracer.Start(tracer.WithDebugMode(false), tracer.WithServiceVersion("v0.0.1"))
	for _, t := range testCases {
		t()
	}
	tracer.Stop()
}
