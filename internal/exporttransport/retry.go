// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package exporttransport provides protocol-neutral request execution helpers for exporters.
package exporttransport

import (
	"context"
	"errors"
	"time"

	"github.com/cenkalti/backoff/v5"
)

// Response is the HTTP response state retained for an export attempt.
type Response struct {
	StatusCode int
	Body       []byte
}

// Attempt reports one execution of an export request.
type Attempt struct {
	Response
	RetryAfter time.Duration
	Retriable  bool
	Err        error
}

// Result reports the final state of a retried export request.
type Result struct {
	Response
	Attempts  int
	Retriable bool
}

// RetryOptions configures [Retry].
type RetryOptions struct {
	MaxAttempts    uint
	MaxElapsedTime time.Duration
	BackOff        backoff.BackOff
}

// Retry executes operation until it succeeds or its retry policy stops.
func Retry(ctx context.Context, opts RetryOptions, operation func(context.Context) Attempt) (Result, error) {
	backOff := opts.BackOff
	if backOff == nil {
		backOff = &backoff.ZeroBackOff{}
	}
	adaptiveBackOff := &retryBackOff{BackOff: backOff}

	var result Result
	attempts := 0
	_, err := backoff.Retry(ctx, func() (Result, error) {
		attempts++
		attempt := operation(ctx)
		result = Result{
			Response:  attempt.Response,
			Attempts:  attempts,
			Retriable: attempt.Retriable,
		}
		if attempt.Err == nil {
			result.Retriable = false
			return result, nil
		}
		if !attempt.Retriable {
			return result, backoff.Permanent(attempt.Err)
		}
		if attempt.RetryAfter > 0 {
			adaptiveBackOff.retryAfter = attempt.RetryAfter
		}
		return result, attempt.Err
	},
		backoff.WithBackOff(adaptiveBackOff),
		backoff.WithMaxTries(opts.MaxAttempts),
		backoff.WithMaxElapsedTime(opts.MaxElapsedTime),
	)
	if err != nil && ctx.Err() != nil {
		result.Retriable = false
	}
	var permanent *backoff.PermanentError
	if errors.As(err, &permanent) {
		err = permanent.Unwrap()
	}
	return result, err
}

type retryBackOff struct {
	backoff.BackOff
	retryAfter time.Duration
}

func (b *retryBackOff) Reset() {
	b.retryAfter = 0
	b.BackOff.Reset()
}

func (b *retryBackOff) NextBackOff() time.Duration {
	delay := b.BackOff.NextBackOff()
	if b.retryAfter > 0 {
		delay = b.retryAfter
		b.retryAfter = 0
	}
	return delay
}
