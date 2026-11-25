// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func TestWrapProduceChannelWithDone(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	tr := NewKafkaTracer(testInstr, CKGoVersion1, 0)
	translate := func(m *simpleMessage) Message { return m }

	t.Run("no produce channel", func(t *testing.T) {
		// With the produce channel disabled, Close waits for done right
		// away, so it must already be closed.
		in, done := WrapProduceChannelWithDone[*simpleMessage](tr, nil, translate)
		assert.Nil(t, in)
		select {
		case <-done:
		default:
			t.Fatal("done must be closed when there is no produce channel")
		}
	})
	t.Run("forwards the messages, then closes done", func(t *testing.T) {
		out := make(chan *simpleMessage, 2)
		in, done := WrapProduceChannelWithDone(tr, out, translate)
		msg := &simpleMessage{tp: simpleTopicPartition{topic: "test-topic"}}
		in <- msg
		in <- msg
		close(in)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("done must be closed after the messages were forwarded")
		}
		require.Len(t, out, 2, "done must be closed only after every message was sent to out")
	})
}
