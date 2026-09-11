// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package exporttransport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testBackOff struct {
	delays []time.Duration
	index  int
	calls  int
	resets int
}

func (b *testBackOff) NextBackOff() time.Duration {
	b.calls++
	if b.index >= len(b.delays) {
		return 0
	}
	delay := b.delays[b.index]
	b.index++
	return delay
}

func (b *testBackOff) Reset() {
	b.resets++
	b.index = 0
}

func TestRetry(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		backOff := &testBackOff{}
		result, err := Retry(context.Background(), RetryOptions{MaxAttempts: 3, BackOff: backOff}, func(context.Context) Attempt {
			return Attempt{Response: Response{StatusCode: 202, Body: []byte("ok")}}
		})
		require.NoError(t, err)
		assert.Equal(t, Result{Response: Response{StatusCode: 202, Body: []byte("ok")}, Attempts: 1}, result)
		assert.Equal(t, 1, backOff.resets)
		assert.Zero(t, backOff.calls)
	})

	t.Run("exhausted", func(t *testing.T) {
		sentinel := errors.New("unavailable")
		backOff := &testBackOff{delays: []time.Duration{0, 0}}
		calls := 0
		result, err := Retry(context.Background(), RetryOptions{MaxAttempts: 3, BackOff: backOff}, func(context.Context) Attempt {
			calls++
			return Attempt{
				Response:  Response{StatusCode: 503, Body: []byte("retry")},
				Retriable: true,
				Err:       sentinel,
			}
		})
		require.ErrorIs(t, err, sentinel)
		assert.Equal(t, 3, calls)
		assert.Equal(t, 3, result.Attempts)
		assert.Equal(t, 503, result.StatusCode)
		assert.Equal(t, []byte("retry"), result.Body)
		assert.True(t, result.Retriable)
	})

	t.Run("permanent", func(t *testing.T) {
		sentinel := errors.New("invalid")
		calls := 0
		result, err := Retry(context.Background(), RetryOptions{MaxAttempts: 1, BackOff: &testBackOff{}}, func(context.Context) Attempt {
			calls++
			return Attempt{Err: sentinel}
		})
		require.ErrorIs(t, err, sentinel)
		assert.Equal(t, sentinel, err)
		assert.Equal(t, 1, calls)
		assert.Equal(t, 1, result.Attempts)
		assert.False(t, result.Retriable)
	})

	t.Run("zero backoff default", func(t *testing.T) {
		calls := 0
		result, err := Retry(context.Background(), RetryOptions{MaxAttempts: 2}, func(context.Context) Attempt {
			calls++
			if calls == 1 {
				return Attempt{Retriable: true, Err: errors.New("unavailable")}
			}
			return Attempt{Response: Response{StatusCode: 200}}
		})
		require.NoError(t, err)
		assert.Equal(t, 2, result.Attempts)
	})

	t.Run("native retry after resets backoff", func(t *testing.T) {
		backOff := &testBackOff{delays: []time.Duration{0}}
		calls := 0
		result, err := Retry(context.Background(), RetryOptions{MaxAttempts: 2, BackOff: backOff}, func(context.Context) Attempt {
			calls++
			if calls == 1 {
				return Attempt{Retriable: true, Err: &backoff.RetryAfterError{Duration: time.Nanosecond}}
			}
			return Attempt{Response: Response{StatusCode: 200}}
		})
		require.NoError(t, err)
		assert.Equal(t, 2, result.Attempts)
		assert.Equal(t, 1, backOff.calls)
		assert.Equal(t, 2, backOff.resets)
	})

	t.Run("retry after override preserves backoff", func(t *testing.T) {
		backOff := &testBackOff{delays: []time.Duration{0}}
		calls := 0
		result, err := Retry(context.Background(), RetryOptions{MaxAttempts: 2, BackOff: backOff}, func(context.Context) Attempt {
			calls++
			if calls == 1 {
				return Attempt{Retriable: true, RetryAfter: time.Nanosecond, Err: errors.New("limited")}
			}
			return Attempt{Response: Response{StatusCode: 200}}
		})
		require.NoError(t, err)
		assert.Equal(t, 2, result.Attempts)
		assert.Equal(t, 1, backOff.calls)
		assert.Equal(t, 1, backOff.resets)
	})

	t.Run("maximum elapsed time", func(t *testing.T) {
		sentinel := errors.New("unavailable")
		backOff := &testBackOff{delays: []time.Duration{time.Hour}}
		result, err := Retry(context.Background(), RetryOptions{
			MaxAttempts:    3,
			MaxElapsedTime: time.Second,
			BackOff:        backOff,
		}, func(context.Context) Attempt {
			return Attempt{Retriable: true, Err: sentinel}
		})
		require.ErrorIs(t, err, sentinel)
		assert.Equal(t, 1, result.Attempts)
		assert.True(t, result.Retriable)
	})
}

func TestRetryCancellationInterruptsWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	resultCh := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := Retry(ctx, RetryOptions{MaxAttempts: 3, BackOff: &testBackOff{delays: []time.Duration{time.Hour}}}, func(context.Context) Attempt {
			close(started)
			return Attempt{Retriable: true, Err: errors.New("unavailable")}
		})
		resultCh <- result
		errCh <- err
	}()
	<-started
	cancel()
	result := <-resultCh
	require.ErrorIs(t, <-errCh, context.Canceled)
	assert.Equal(t, 1, result.Attempts)
	assert.False(t, result.Retriable)
}
