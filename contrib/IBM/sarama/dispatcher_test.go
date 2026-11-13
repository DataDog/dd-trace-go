// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

// fakeDispatcher lets tests drive wrappedDispatcher.Run() with synthetic
// messages, without a live Kafka broker.
type fakeDispatcher struct {
	ch chan *sarama.ConsumerMessage
}

func (f *fakeDispatcher) Messages() <-chan *sarama.ConsumerMessage { return f.ch }

// TestWrappedDispatcherCustomTagPrecedence is a regression test for a Codex
// review finding on PR #5007: WithConsumerCustomTag lets a caller override a
// tag also set by the cached spanCfg base (e.g. component, span.kind).
// Custom tags must win on key collision, matching pre-migration behavior
// where custom-tag options were appended last in the option list.
func TestWrappedDispatcherCustomTagPrecedence(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	cfg := new(config)
	defaults(cfg)
	cfg.consumerCustomTags[ext.Component] = func(*sarama.ConsumerMessage) any {
		return "custom-component"
	}

	fd := &fakeDispatcher{ch: make(chan *sarama.ConsumerMessage)}
	wd := wrapDispatcher(fd, cfg)

	done := make(chan struct{})
	go func() {
		wd.Run()
		close(done)
	}()

	fd.ch <- &sarama.ConsumerMessage{Topic: "test-topic"}
	<-wd.Messages() // unblock Run()'s re-emit send so it can loop back around
	close(fd.ch)
	<-done

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "custom-component", spans[0].Tag(ext.Component))
}
