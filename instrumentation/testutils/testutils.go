// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package testutils

import (
	"runtime"
	"sync"
	"testing"
	"unsafe"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func SetGlobalServiceName(t *testing.T, val string) {
	t.Helper()
	prev := globalconfig.ServiceName()
	t.Cleanup(func() {
		globalconfig.SetServiceName(prev)
	})
	globalconfig.SetServiceName(val)
}

func SetGlobalAnalyticsRate(t *testing.T, val float64) {
	t.Helper()
	prev := globalconfig.AnalyticsRate()
	t.Cleanup(func() {
		globalconfig.SetAnalyticsRate(prev)
	})
	globalconfig.SetAnalyticsRate(val)
}

func SetGlobalDogstatsdAddr(t *testing.T, val string) {
	t.Helper()
	prev := globalconfig.DogstatsdAddr()
	t.Cleanup(func() {
		globalconfig.SetDogstatsdAddr(prev)
	})
	globalconfig.SetDogstatsdAddr(val)
}

func SetGlobalHeaderTags(t *testing.T, headers ...string) {
	t.Helper()

	setValue := func(val []string) {
		globalconfig.ClearHeaderTags()
		for _, h := range val {
			header, tag := normalizer.HeaderTag(h)
			globalconfig.SetHeaderTag(header, tag)
		}
	}

	var prev []string
	globalconfig.HeaderTagMap().Iter(func(_ string, tag string) {
		prev = append(prev, tag)
	})

	t.Cleanup(func() {
		setValue(prev)
	})
	setValue(headers)
}

func StartAppSec(t *testing.T, opts ...config.StartOption) {
	appsec.Start(opts...)

	if !appsec.Enabled() {
		if runtime.GOOS != "windows" {
			t.Log("Skipping AppSec test on unsupported platform")
			t.SkipNow()
		}
		t.Fatal("Failed to start AppSec while platform should be supported")
		t.FailNow()
	}

	t.Cleanup(appsec.Stop)
}

func StartAppSecBench(b *testing.B) {
	// maximize rate limit to prevent the spam of "too many WAF events" errors
	// 1000000000 = time.Second.Nanoseconds() is the largest value that we are able to set here
	b.Setenv("DD_APPSEC_TRACE_RATE_LIMIT", "1000000000")
	appsec.Start()
	b.Cleanup(appsec.Stop)
}

type discardLogger struct{}

func (d discardLogger) Log(_ string) {}

func DiscardLogger() tracer.Logger {
	return discardLogger{}
}

type MockStatsdClient = statsdtest.TestStatsdClient

func NewMockStatsdClient() *MockStatsdClient {
	return &MockStatsdClient{}
}

// SetPropagatingTag sets a tag on the given span context. It assumes it comes from a span,
// so it has a trace attached to it.
func SetPropagatingTag(t testing.TB, ctx *tracer.SpanContext, k, v string) {
	t.Helper()

	// Forgive us for the following hack, oh great and powerful GODpher.
	// Assuming the context contains a trace, we extract it by cookie-cutting it.
	// It's easier than using offsets when the desired data isn't far away from
	// the struct's beginning.
	type cookieCutter struct {
		_     bool // spanContext.updated
		trace *struct {
			_               sync.RWMutex      // trace.mu
			_               []any             // trace.spans
			_               map[string]string // trace.tags
			propagatingTags map[string]string // trace level tags that will be propagated across service boundaries
		}
	}
	ptr := uintptr(unsafe.Pointer(ctx))
	cc := (*cookieCutter)(*(*unsafe.Pointer)(unsafe.Pointer(&ptr)))
	cc.trace.propagatingTags[k] = v
}

// FlushTelemetry flushes any pending telemetry data.
func FlushTelemetry() {
	if client := telemetry.GlobalClient(); client != nil {
		client.Flush()
	}
}
