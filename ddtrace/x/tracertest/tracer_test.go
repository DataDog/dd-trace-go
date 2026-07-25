// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracertest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
)

func TestStartReturnsUsablePublicTracer(t *testing.T) {
	agent, err := tracertest.StartAgent(t)
	require.NoError(t, err)

	started, err := tracertest.Start(t, agent,
		tracer.WithService("public-linkname-test"),
		tracer.WithLogStartup(false),
	)
	require.NoError(t, err)
	require.NotNil(t, started)
	assert.Equal(t, "public-linkname-test", started.TracerConf().ServiceTag)
	started.Flush()
}
