// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"context"
	"os"
	"testing"
	"time"
	
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/elasticsearch"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartElasticsearchV6Container starts a new StartElasticsearch V6 test container.
func StartElasticsearchV6Container(t testing.TB) *elasticsearch.ElasticsearchContainer {
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithLogger(testcontainers.TestLogger(t)),
		WithTestLogConsumer(t),
		testcontainers.WithWaitStrategyAndDeadline(time.Minute, wait.ForLog(`.*("message":\s?"started(\s|")?.*|]\sstarted\n)`).AsRegexp()),
	}
	if _, ok := os.LookupEnv("CI"); ok {
		t.Log("attempting to reuse elasticsearch6 container in CI")
		opts = append(opts, testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     "elasticsearch6",
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}))
	}

	container, err := elasticsearch.Run(ctx,
		"docker.elastic.co/elasticsearch/elasticsearch:6.8.23", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
		opts...,
	)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	return container
}

// StartElasticsearchV7Container starts a new StartElasticsearch V7 test container.
func StartElasticsearchV7Container(t testing.TB) *elasticsearch.ElasticsearchContainer {
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithLogger(testcontainers.TestLogger(t)),
		WithTestLogConsumer(t),
		testcontainers.WithWaitStrategyAndDeadline(time.Minute, wait.ForLog(`.*("message":\s?"started(\s|")?.*|]\sstarted\n)`).AsRegexp()),
	}
	if _, ok := os.LookupEnv("CI"); ok {
		t.Log("attempting to reuse elasticsearch7 container in CI")
		opts = append(opts, testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     "elasticsearch7",
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}))
	}

	container, err := elasticsearch.Run(ctx,
		"docker.elastic.co/elasticsearch/elasticsearch:7.1.24", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
		opts...,
	)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	return container
}

// StartElasticsearchV8Container starts a new StartElasticsearch V8 test container.
func StartElasticsearchV8Container(t testing.TB) *elasticsearch.ElasticsearchContainer {
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithLogger(testcontainers.TestLogger(t)),
		WithTestLogConsumer(t),
		testcontainers.WithWaitStrategyAndDeadline(time.Minute, wait.ForLog(`.*("message":\s?"started(\s|")?.*|]\sstarted\n)`).AsRegexp()),
	}
	if _, ok := os.LookupEnv("CI"); ok {
		t.Log("attempting to reuse elasticsearch8 container in CI")
		opts = append(opts, testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     "elasticsearch8",
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}))
	}

	container, err := elasticsearch.Run(ctx,
		"docker.elastic.co/elasticsearch/elasticsearch:8.15.3", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
		opts...,
	)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	return container
}
