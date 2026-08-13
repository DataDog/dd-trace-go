// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

// TestStartConsumeSpanCustomTagPrecedence is a regression test for a Codex
// review finding on PR #5007: WithCustomTag lets a caller override a tag
// also set by the cached consumerSpanCfg base (e.g. component, span.kind).
// Custom tags must win on key collision, matching pre-migration behavior
// where custom-tag options were appended last in the option list.
func TestStartConsumeSpanCustomTagPrecedence(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewKafkaTracer(testInstr, CKGoVersion1, 0, WithCustomTag(ext.Component, func(Message) any {
		return "custom-component"
	}))
	msg := &simpleMessage{tp: simpleTopicPartition{topic: "test-topic"}}

	span := tr.StartConsumeSpan(msg)
	span.Finish()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "custom-component", spans[0].Tag(ext.Component))
}
