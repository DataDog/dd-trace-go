// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracing

import (
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/stretchr/testify/assert"
)

func intPtr(val int) *int {
	return &val
}

var (
	testMsg = pubsub.Message{
		ID:   "id",
		Data: []byte("data"),
		Attributes: map[string]string{
			"key1": "val1",
			"key2": "val2",
		},
		PublishTime:     time.Now(),
		DeliveryAttempt: intPtr(2),
		OrderingKey:     "key",
	}
)

func Test_newPubsubMessage(t *testing.T) {
	{
		// pubsub.Message
		msg := testMsg
		tMsg := newPubsubMessage(&msg)
		assert.Equal(t, "id", tMsg.ID)
		assert.Equal(t, []byte("data"), tMsg.Data)
		assert.Equal(t, map[string]string{
			"key1": "val1",
			"key2": "val2",
		}, tMsg.Attributes)
		assert.NotEmpty(t, tMsg.PublishTime)
		assert.Equal(t, intPtr(2), tMsg.DeliveryAttempt)
		assert.Equal(t, "key", tMsg.OrderingKey)
	}
	{
		// not a struct
		msg := newPubsubMessage(map[string]string{})
		assert.Empty(t, msg)
	}
	{
		// different type
		type otherMsg struct {
			ID          string
			Data        int
			OrderingKey string
			OtherField  int
		}
		msg := newPubsubMessage(&otherMsg{
			ID:          "other-id",
			Data:        123,
			OrderingKey: "other-key",
			OtherField:  321,
		})
		assert.Equal(t, &pubsubMsg{
			ID:              "other-id",
			Data:            nil,
			OrderingKey:     "other-key",
			Attributes:      nil,
			DeliveryAttempt: nil,
			PublishTime:     time.Time{},
		}, msg)
	}
}

func Test_setAttributes(t *testing.T) {
	{
		// pubsub.Message
		msg := testMsg
		setAttributes(&msg, map[string]string{"key3": "val3"})
		assert.Equal(t, map[string]string{"key3": "val3"}, msg.Attributes)
	}
	{
		// not a struct
		msg := map[string]string{}
		setAttributes(&msg, map[string]string{"key3": "val3"})
		assert.Empty(t, msg)
	}
	{
		// different type
		type otherMsg struct {
			ID          string
			Data        int
			OrderingKey string
			OtherField  int
		}
		msg := &otherMsg{
			ID:          "other-id",
			Data:        123,
			OrderingKey: "other-key",
			OtherField:  321,
		}
		setAttributes(msg, map[string]string{"key3": "val3"})

		// here we just test it does not panic
	}
}
