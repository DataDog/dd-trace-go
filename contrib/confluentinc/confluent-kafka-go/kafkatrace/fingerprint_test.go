// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEdgeFingerprint(t *testing.T) {
	base := edgeFingerprint("in", "orders", "grp", "clusterA")

	// Deterministic for identical inputs within a process.
	assert.Equal(t, base, edgeFingerprint("in", "orders", "grp", "clusterA"))

	// Each field contributes to the fingerprint.
	assert.NotEqual(t, base, edgeFingerprint("out", "orders", "grp", "clusterA"), "direction")
	assert.NotEqual(t, base, edgeFingerprint("in", "payments", "grp", "clusterA"), "topic")
	assert.NotEqual(t, base, edgeFingerprint("in", "orders", "grp2", "clusterA"), "group")
	assert.NotEqual(t, base, edgeFingerprint("in", "orders", "grp", "clusterB"), "cluster")

	// Separators disambiguate field boundaries: (topic="ab",group="c") must not
	// collide with (topic="a",group="bc").
	assert.NotEqual(t, edgeFingerprint("in", "ab", "c", ""), edgeFingerprint("in", "a", "bc", ""))
}
