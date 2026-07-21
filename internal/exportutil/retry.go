// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package exportutil

import (
	"context"
	"math/rand/v2"
	"net/http"
	"time"
)

const (
	initialBackoff = 100 * time.Millisecond
	maxBackoff     = time.Second
)

// jitter applies equal-jitter to d (half fixed, half random in [0, d/2]) so that
// many exporters hitting the same transient failure do not retry in lockstep.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

// Result reports the outcome of a bounded Retry over a single request's POST.
type Result struct {
	StatusCode int
	Attempts   int
	Body       []byte
	Retriable  bool
}

// Retriable classifies a failed attempt. A caller-cancelled/expired context is
// not transient; status 0 (network error), 408, 429 and 5xx are transient;
// other 4xx are permanent.
func Retriable(ctx context.Context, status int) bool {
	if ctx.Err() != nil {
		return false
	}
	switch {
	case status == 0:
		return true
	case status == http.StatusRequestTimeout, status == http.StatusTooManyRequests:
		return true
	case status >= 500 && status <= 599:
		return true
	default:
		return false
	}
}

// Attempt is the outcome of a single do() call inside Retry.
type Attempt struct {
	// Status is the HTTP status (0 for a network-level error).
	Status int
	// Body is the (bounded) response body.
	Body []byte
	// RetryAfter is a server-requested minimum delay before the next attempt
	// (e.g. parsed from a Retry-After header); 0 falls back to exponential
	// backoff. It is only honored when the attempt is retriable.
	RetryAfter time.Duration
	// Err is nil on success.
	Err error
}

// RetryOptions configures Retry.
type RetryOptions struct {
	// MaxAttempts bounds the total number of do() calls (>=1).
	MaxAttempts uint
	// Retriable classifies a failed status as transient. Defaults to Retriable
	// when nil, letting callers with stricter rules (e.g. the OTLP/HTTP spec)
	// override the classification.
	Retriable func(ctx context.Context, status int) bool
}

// Retry runs do up to opts.MaxAttempts times with exponential backoff, retrying
// only transient failures (per opts.Retriable). A retriable attempt that reports
// a RetryAfter waits at least that long instead of the backoff. It returns a
// structured Result plus the error from the final attempt (nil on success).
func Retry(ctx context.Context, opts RetryOptions, do func(context.Context) Attempt) (Result, error) {
	retriable := opts.Retriable
	if retriable == nil {
		retriable = Retriable
	}
	var res Result
	backoff := initialBackoff
	for attempt := 1; ; attempt++ {
		res.Attempts = attempt
		a := do(ctx)
		res.StatusCode = a.Status
		res.Body = a.Body
		if a.Err == nil {
			res.Retriable = false
			return res, nil
		}
		res.Retriable = retriable(ctx, a.Status)
		if !res.Retriable || uint(attempt) >= opts.MaxAttempts {
			return res, a.Err
		}
		wait := jitter(backoff)
		if a.RetryAfter > 0 {
			wait = a.RetryAfter
		}
		select {
		case <-ctx.Done():
			res.Retriable = false
			return res, ctx.Err()
		case <-time.After(wait):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}
