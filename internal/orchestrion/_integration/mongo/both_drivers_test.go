// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package mongo

import (
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo/options"
	optionsv2 "go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestBothDriverVersions checks that a file calling options.Client from both
// driver versions builds, as during a migration from v1 to v2, and that each
// call gets the monitor of its own version.
func TestBothDriverVersions(t *testing.T) {
	require.True(t, built.WithOrchestrion || otelc.Enabled(),
		"this test must be run with either orchestrion or otelc enabled")
	require.NotNil(t, options.Client().Monitor)
	require.NotNil(t, optionsv2.Client().Monitor)
}
