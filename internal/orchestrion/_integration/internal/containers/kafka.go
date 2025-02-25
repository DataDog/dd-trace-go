// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartKafkaTestContainer starts a new Kafka test container and returns the connection string.
func StartKafkaTestContainer(t testing.TB) (*kafka.KafkaContainer, string) {
	ctx := context.Background()
	exposedPort := "9093/tcp"

	opts := []testcontainers.ContainerCustomizer{
		kafka.WithClusterID("test-cluster"),
		WithTestLogConsumer(t),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForListeningPort(nat.Port(exposedPort)),
				wait.ForExec(createTopicCmd("topic-A")),
				wait.ForExec(createTopicCmd("topic-B")),
				wait.ForExec(checkTopicExistsCmd("topic-A")),
				wait.ForExec(checkTopicExistsCmd("topic-B")),
			),
		),
	}
	if _, ok := os.LookupEnv("CI"); ok {
		t.Log("attempting to reuse kafka container in CI")
		opts = append(opts, testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     "kafka",
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}))
	}
	container, err := kafka.Run(ctx,
		"confluentinc/confluent-local:7.5.0", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
		opts...,
	)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	mappedPort, err := container.MappedPort(ctx, nat.Port(exposedPort))
	require.NoError(t, err)

	host, err := container.Host(ctx)
	require.NoError(t, err)

	addr := fmt.Sprintf("%s:%s", host, mappedPort.Port())
	return container, addr
}

func createTopicCmd(topic string) []string {
	return []string{
		"kafka-topics",
		"--bootstrap-server", "localhost:9092",
		"--topic", topic,
		"--create",
		"--if-not-exists",
		"--partitions", "1",
		"--replication-factor", "1",
	}
}

func checkTopicExistsCmd(topic string) []string {
	return []string{
		"kafka-topics",
		"--bootstrap-server",
		"localhost:9092",
		"--list",
		"|",
		"grep", topic,
	}
}
