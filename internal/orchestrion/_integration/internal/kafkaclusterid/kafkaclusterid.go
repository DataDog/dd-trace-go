// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package kafkaclusterid helps the confluent-kafka-go tests wait for the
// cluster ID lookup of a traced client.
package kafkaclusterid

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/contrib/confluentinc/confluent-kafka-go/kafkatrace"
)

// Found returns how many cluster ID lookups have found a cluster ID so far.
// Pass it to Wait.
func Found() int64 {
	return kafkatrace.ClusterIDsFound()
}

// Wait waits until a cluster ID lookup has found a cluster ID since Found
// returned before, so that the messages of the client that started it are
// tagged with the cluster ID from then on.
func Wait(t testing.TB, before int64) {
	t.Helper()
	require.Eventually(t, func() bool { return kafkatrace.ClusterIDsFound() > before },
		30*time.Second, 10*time.Millisecond, "the cluster ID lookup did not find a cluster ID")
}
