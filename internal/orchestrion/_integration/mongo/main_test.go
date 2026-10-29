// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package mongo

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

func TestClientInMain(t *testing.T) {
	maintest.RequireOtelc(t)
	containers.SkipIfProviderIsNotHealthy(t)
	_, uri := containers.StartMongoDBTestContainer(t)
	t.Setenv("MONGODB_URI", uri)

	agent := maintest.Run(t, "./mongo/mainapp")
	assert.Equal(t, map[string]int{"main": 1, "alias": 1}, insertsByDB(t, agent.Agent))
}
