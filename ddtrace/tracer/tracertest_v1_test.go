// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleV1TracesDecodesEncodedPayload covers handleV1Traces against a real
// encoded v1.0 payload. The handler is registered by startAgentTest but has
// never been reachable — the protocol gate always selects v0.4 for that mock —
// so it went unexercised, and its bare &payloadV1{} literal left header nil,
// panicking inside updateHeader on the first decode.
func TestHandleV1TracesDecodesEncodedPayload(t *testing.T) {
	p := newPayload(traceProtocolV1)
	require.Equal(t, traceProtocolV1, p.protocol())

	span := newSpan("v1.op", "v1.service", "v1.resource", 0, 0, 0)
	_, err := p.push([]*Span{span})
	require.NoError(t, err)

	body, err := io.ReadAll(p)
	require.NoError(t, err)
	require.NotEmpty(t, body)

	spans := handleV1Traces(bytes.NewReader(body))
	require.Len(t, spans, 1, "handleV1Traces must decode the payload it was handed")
	assert.Equal(t, "v1.op", spans[0].Operation)
	assert.Equal(t, "v1.service", spans[0].Service)
	assert.Equal(t, "v1.resource", spans[0].Resource)
}
