package opentelemetry

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func BenchmarkBridgeSpanFromContext(b *testing.B) {
	if err := tracer.Start(); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { tracer.Stop() })
	loadBridge()
	prev := bridgeEnabled
	bridgeEnabled = true
	b.Cleanup(func() { bridgeEnabled = prev })
	ddSpan, ctx := tracer.StartSpanFromContext(context.Background(), "dd.op")
	bindOwner(ddSpan)
	defer ddSpan.Finish()

	b.ReportAllocs()
	for b.Loop() {
		sp, ok := bridgeSpanFromContext(ctx, nil)
		if !ok || sp == nil {
			b.Fatal("missing span")
		}
	}
}
