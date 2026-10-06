// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package grpc

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCachedServiceNameConcurrentFirstUse covers interceptors that are built
// before the tracer starts: the warm-up in newCachedServiceName cannot cache
// the value, so the first concurrent calls to String() after the tracer starts
// race to publish it. Run with -race; the torn-header check additionally
// catches a reader observing a string with a nil data pointer and a non-zero
// length, which is what crashed tracer.StartSpan in string comparison.
func TestCachedServiceNameConcurrentFirstUse(t *testing.T) {
	const (
		want       = "grpc-client-service"
		goroutines = 16
		rounds     = 2000
	)

	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil), tracer.WithLogStartup(false)))
	defer tracer.Stop()

	var torn atomic.Int64
	for range rounds {
		// The literal has the state newCachedServiceName leaves behind when the
		// interceptor is built before the tracer starts: nothing cached yet.
		cs := &cachedServiceName{getValue: func() string { return strings.Clone(want) }}

		var (
			wg    sync.WaitGroup
			start = make(chan struct{})
		)
		for range goroutines {
			wg.Go(func() {
				<-start
				got := cs.String()
				if len(got) > 0 && unsafe.StringData(got) == nil {
					torn.Add(1)
					return
				}
				assert.Equal(t, want, got)
			})
		}
		close(start)
		wg.Wait()
	}
	assert.Zero(t, torn.Load(), "String() returned a string with nil data and non-zero length")
}
