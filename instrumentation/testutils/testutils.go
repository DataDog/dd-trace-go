package testutils

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"
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

type discardLogger struct{}

func (d discardLogger) Log(_ string) {}

func DiscardLogger() tracer.Logger {
	return discardLogger{}
}
