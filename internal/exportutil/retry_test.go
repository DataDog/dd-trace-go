// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package exportutil

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetry_SucceedsFirstTry(t *testing.T) {
	calls := 0
	res, err := Retry(context.Background(), RetryOptions{MaxAttempts: 3}, func(context.Context) Attempt {
		calls++
		return Attempt{Status: 200, Body: []byte("ok")}
	})
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, 1, res.Attempts)
	assert.False(t, res.Retriable)
	assert.Equal(t, 200, res.StatusCode)
}

func TestRetry_StopsAtMaxAttemptsWithDefaultClassifier(t *testing.T) {
	calls := 0
	res, err := Retry(context.Background(), RetryOptions{MaxAttempts: 3}, func(context.Context) Attempt {
		calls++
		return Attempt{Status: 503, Err: errors.New("unavailable")} // default Retriable retries 5xx
	})
	require.Error(t, err)
	assert.Equal(t, 3, calls) // exhausted every attempt
	assert.Equal(t, 3, res.Attempts)
	assert.True(t, res.Retriable)
	assert.Equal(t, 503, res.StatusCode)
}

func TestRetry_NonRetriableStopsImmediately(t *testing.T) {
	calls := 0
	res, err := Retry(context.Background(), RetryOptions{MaxAttempts: 3}, func(context.Context) Attempt {
		calls++
		return Attempt{Status: 400, Err: errors.New("bad request")}
	})
	require.Error(t, err)
	assert.Equal(t, 1, calls) // 400 is permanent under the default classifier
	assert.False(t, res.Retriable)
}

func TestRetry_HonorsRetryAfterOverBackoff(t *testing.T) {
	calls := 0
	start := time.Now()
	_, err := Retry(context.Background(), RetryOptions{MaxAttempts: 2}, func(context.Context) Attempt {
		calls++
		if calls == 1 {
			return Attempt{Status: 503, RetryAfter: 400 * time.Millisecond, Err: errors.New("throttled")}
		}
		return Attempt{Status: 200}
	})
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	// The wait must honor RetryAfter (~400ms), not the ~100ms exponential backoff.
	assert.GreaterOrEqual(t, time.Since(start), 350*time.Millisecond)
}

func TestRetry_ContextCancelInterruptsWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, err := Retry(ctx, RetryOptions{MaxAttempts: 5}, func(context.Context) Attempt {
		return Attempt{Status: 503, RetryAfter: 10 * time.Second, Err: errors.New("throttled")}
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 2*time.Second) // did not sleep the full 10s
}

func TestRetry_CustomRetriablePredicate(t *testing.T) {
	only418 := func(_ context.Context, status int) bool { return status == 418 }

	calls := 0
	_, err := Retry(context.Background(), RetryOptions{MaxAttempts: 3, Retriable: only418}, func(context.Context) Attempt {
		calls++
		return Attempt{Status: 500, Err: errors.New("err")} // not retriable under this predicate
	})
	require.Error(t, err)
	assert.Equal(t, 1, calls)

	calls = 0
	_, err = Retry(context.Background(), RetryOptions{MaxAttempts: 3, Retriable: only418}, func(context.Context) Attempt {
		calls++
		return Attempt{Status: 418, Err: errors.New("teapot")}
	})
	require.Error(t, err)
	assert.Equal(t, 3, calls) // 418 retries under this predicate
}
