// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"context"
	"testing"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/contrib/confluentinc/confluent-kafka-go/kafkatrace"
)

// TestClusterIDClientClosed checks that the cluster ID lookup does not use the
// native handle of a consumer closed without the wrapper's Close.
func TestClusterIDClientClosed(t *testing.T) {
	c, err := kafka.NewConsumer(&kafka.ConfigMap{
		"bootstrap.servers": "127.0.0.1:1",
		"group.id":          "test-cluster-id-closed",
	})
	require.NoError(t, err)
	admin, err := kafka.NewAdminClientFromConsumer(c)
	require.NoError(t, err)
	client := clusterIDClient{AdminClient: admin, closed: c.IsClosed}

	require.NoError(t, c.Close())
	_, err = client.ClusterID(context.Background())
	assert.ErrorIs(t, err, kafkatrace.ErrClientClosed)
}
