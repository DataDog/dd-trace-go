// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coveragepropagation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type recordingPropagator struct {
	sent, received []uint64
	forgot, moved  int
}

func (p *recordingPropagator) Sent(spanID uint64) func() {
	p.sent = append(p.sent, spanID)
	return func() { p.forgot++ }
}

func (p *recordingPropagator) Received(spanID uint64) func() {
	p.received = append(p.received, spanID)
	return func() { p.moved++ }
}

func TestPropagation(t *testing.T) {
	t.Cleanup(func() { Set(nil) })

	// Without a propagator, both calls do nothing.
	Sent(1)()
	Received(1)()

	p := &recordingPropagator{}
	Set(p)
	Sent(2)()
	Received(3)()
	assert.Equal(t, []uint64{2}, p.sent)
	assert.Equal(t, []uint64{3}, p.received)
	assert.Equal(t, 1, p.forgot)
	assert.Equal(t, 1, p.moved)

	Set(nil)
	Sent(4)()
	Received(5)()
	assert.Equal(t, []uint64{2}, p.sent)
	assert.Equal(t, []uint64{3}, p.received)
}
