// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkaclusterid

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFetcherSuccess(t *testing.T) {
	var f Fetcher
	f.FetchAsync(func(ctx context.Context) string {
		return "test-cluster-id"
	})
	f.Wait()
	assert.Equal(t, "test-cluster-id", f.ID())
}

func TestFetcherEmptyResult(t *testing.T) {
	var f Fetcher
	f.FetchAsync(func(ctx context.Context) string {
		return ""
	})
	f.Wait()
	assert.Equal(t, "", f.ID())
}

func TestFetcherStopCancelsContext(t *testing.T) {
	var f Fetcher
	started := make(chan struct{})
	f.FetchAsync(func(ctx context.Context) string {
		close(started)
		<-ctx.Done()
		return ""
	})
	<-started
	// Stop should cancel the context and return quickly.
	done := make(chan struct{})
	go func() {
		f.Stop()
		close(done)
	}()
	select {
	case <-done:
		// success
	case <-time.After(time.Second):
		t.Fatal("Stop did not return in time")
	}
}

func TestFetcherStopAfterCompletion(t *testing.T) {
	var f Fetcher
	f.FetchAsync(func(ctx context.Context) string {
		return "abc"
	})
	f.Wait()
	// Stop after completion should be a no-op.
	f.Stop()
	assert.Equal(t, "abc", f.ID())
}

func TestFetcherStopWithoutFetch(t *testing.T) {
	var f Fetcher
	// Stop on a fetcher that was never started should not panic.
	f.Stop()
	assert.Equal(t, "", f.ID())
}
