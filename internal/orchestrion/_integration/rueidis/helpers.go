// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package rueidis

import (
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/redis/rueidis"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// startRedis starts the Redis container. Call it from PreBootstrap, before the
// tracer starts, so the Docker API calls it makes are not counted.
func startRedis(t *testing.T) string {
	containers.SkipIfProviderIsNotHealthy(t)
	_, addr := containers.StartRedisTestContainer(t)
	return addr
}

// clientOption returns the options of a client that connects to addr.
func clientOption(addr string) rueidis.ClientOption {
	return rueidis.ClientOption{InitAddress: []string{addr}}
}

// rootWithCommand is a test.root span with one redis.command span for a GET,
// made by a client with the given service name.
func rootWithCommand(service string) trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "redis.command",
						"service":  service,
						"resource": "GET",
						"type":     "redis",
					},
					Meta: map[string]string{
						"component": "redis/rueidis",
						"db.system": "redis",
						"out.host":  "localhost",
						"span.kind": "client",
					},
				},
			},
		},
	}
}
