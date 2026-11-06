// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package pgx

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestMainApp covers pgx constructors called from package main, including one
// imported under another name. The harness suites never build a package main.
func TestMainApp(t *testing.T) {
	agent := maintest.Run(t, "./pgx/mainapp")

	connect := agent.RequireSpan(t, agenttest.With().
		Operation("pgx.connect").
		Tag("component", "jackc/pgx.v5"))
	assert.NotZero(t, connect.Error, "the connect span must carry the error")
	agent.RequireSpan(t, agenttest.With().
		Operation("pgx.pool.acquire").
		Tag("component", "jackc/pgx.v5"))

	// A connect span for each of the two pgx.Connect calls and pgx.ConnectConfig,
	// and an acquire and a connect span for each pool.
	assert.Equal(t, 7, agent.CountSpans())
}
