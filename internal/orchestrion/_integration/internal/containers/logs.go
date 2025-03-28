// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

func WithTestLogConsumer(t testing.TB) testcontainers.CustomizeRequestOption {
	return testcontainers.WithLogConsumers(TestLogConsumer(t))
}

type testLogConsumer struct {
	testing.TB
}

func TestLogConsumer(t testing.TB) testcontainers.LogConsumer {
	return testLogConsumer{t}
}

func (t testLogConsumer) Accept(log testcontainers.Log) {
	t.TB.Log(string(log.Content))
}
