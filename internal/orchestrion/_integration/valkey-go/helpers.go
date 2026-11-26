// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package valkey

import (
	"context"
	"testing"

	"github.com/valkey-io/valkey-go"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// Aliases that let only_import.go name the valkey-go types without importing
// the package.
type (
	clientOption = valkey.ClientOption
	valkeyClient = valkey.Client
)

// startValkey starts the container before the tracer, so the spans of the
// Docker API calls are not counted.
func startValkey(t *testing.T) string {
	containers.SkipIfProviderIsNotHealthy(t)
	_, addr := containers.StartValkeyTestContainer(t)
	return addr
}

// valkeyServer is embedded by test cases that need a server and an exact span
// count.
type valkeyServer struct {
	addr string
}

func (s *valkeyServer) PreBootstrap(_ context.Context, t *testing.T) {
	s.addr = startValkey(t)
}

func commandSpan(service, resource string) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "valkey.command",
			"service":  service,
			"resource": resource,
			"type":     "valkey",
		},
		Meta: map[string]string{
			"component": "valkey-io/valkey-go",
			"db.system": "valkey",
			"out.db":    "0",
			"out.host":  "localhost",
			"span.kind": "client",
		},
	}
}
