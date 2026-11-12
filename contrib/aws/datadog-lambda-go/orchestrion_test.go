// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ddlambda

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWrapOrchestrionSkipsManuallyWrappedHandler(t *testing.T) {
	t.Setenv(UniversalInstrumentation, "false")
	t.Setenv(DatadogTraceEnabledEnvVar, "false")
	t.Cleanup(func() { manuallyWrapped.Store(false) })

	WrapFunction(func(context.Context) error { return nil }, nil)

	handler := func(context.Context, []byte) (io.Reader, error) {
		return bytes.NewBufferString("{}"), nil
	}
	got := wrapOrchestrion(handler)
	assert.Equal(t, reflect.ValueOf(handler).Pointer(), reflect.ValueOf(got).Pointer())
}
