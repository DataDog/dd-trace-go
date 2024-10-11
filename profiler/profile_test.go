// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_goroutineDebug2ToPprof_CrashSafety(t *testing.T) {
	err := goroutineDebug2ToPprof(panicReader{}, io.Discard, time.Time{})
	require.NotNil(t, err)
	require.Equal(t, "panic: 42", err.Error())
}

// panicReader is used to create a panic inside of stackparse.Parse()
type panicReader struct{}

func (c panicReader) Read(_ []byte) (int, error) {
	panic("42")
}
