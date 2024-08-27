// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package testutils

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"
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

func StartAppSec(t *testing.T) {
	appsec.Start()
	t.Cleanup(appsec.Stop)
}

func StartAppSecBench(b *testing.B) {
	appsec.Start()
	b.Cleanup(appsec.Stop)
}

type discardLogger struct{}

func (d discardLogger) Log(_ string) {}

func DiscardLogger() tracer.Logger {
	return discardLogger{}
}

type MockStatsdClient = statsdtest.TestStatsdClient

func NewMockStatsdClient() MockStatsdClient {
	return MockStatsdClient{}
}
