// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"context"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/errortrace"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/stretchr/testify/assert"
)

type TestCase struct{}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	err := generateError()
	tracerErr := generateTracerError()

	assert.IsType(t, (*error)(nil), err)
	assert.IsType(t, &errortrace.TracerError{}, tracerErr)
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Meta: map[string]string{
				"error.details": "test error",
				"error.type":    "*errors.errorString",
				"error.message": "test error",
			},
		},
		{
			Meta: map[string]string{
				"error.details": "test error",
				"error.type":    "errortrace.TracerError",
				"error.message": "test error",
			},
		},
	}
}

func generateError() error {
	return fmt.Errorf("test error")
}

func generateTracerError() *errortrace.TracerError {
	return errortrace.New("test error")
}
