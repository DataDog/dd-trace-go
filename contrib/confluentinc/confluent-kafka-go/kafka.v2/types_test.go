// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"testing"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/stretchr/testify/assert"
)

func TestTopicPartitionErrorIsGenericServerError(t *testing.T) {
	t.Run("nil error returns false", func(t *testing.T) {
		wErr := wTopicPartitionError{nil}
		assert.False(t, wErr.IsGenericServerError())
	})

	t.Run("generic server error returns true", func(t *testing.T) {
		wErr := wTopicPartitionError{kafka.NewError(kafka.ErrUnknown, "unknown error", false)}
		assert.True(t, wErr.IsGenericServerError())
	})

	t.Run("non-generic error returns false", func(t *testing.T) {
		wErr := wTopicPartitionError{kafka.NewError(kafka.ErrInvalidArg, "invalid argument", false)}
		assert.False(t, wErr.IsGenericServerError())
	})
}
