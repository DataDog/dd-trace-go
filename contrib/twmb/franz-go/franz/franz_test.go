// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package franz

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestWrapClient(t *testing.T) {
	client, err := kgo.NewClient()
	assert.NoError(t, err)
	wrapped := WrapClient(client)
	assert.NotNil(t, wrapped)
	assert.Equal(t, client, wrapped.Client)
}

func TestNewClient(t *testing.T) {
	client, err := NewClient([]kgo.Opt{})
	assert.NoError(t, err)
	assert.NotNil(t, client)
}

func TestHeaders(t *testing.T) {
	headers := []kgo.RecordHeader{
		{Key: "key1", Value: []byte("value1")},
		{Key: "key2", Value: []byte("value2")},
	}
	carrier := NewRecordHeadersCarrier(headers)

	// Test ForeachKey
	var count int
	err := carrier.ForeachKey(func(key, val string) error {
		count++
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, count)

	// Test Set
	carrier.Set("key3", "value3")
	assert.Equal(t, 3, len(carrier.GetHeaders()))

	// Test overwrite
	carrier.Set("key1", "newvalue1")
	assert.Equal(t, 3, len(carrier.GetHeaders()))
	for _, h := range carrier.GetHeaders() {
		if h.Key == "key1" {
			assert.Equal(t, "newvalue1", string(h.Value))
		}
	}
}
