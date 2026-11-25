// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fake runtime runs one goroutine at a time: setting rt.slot directly
// switches to another goroutine, which carries that slot.

func TestLineCoverageRequestFromTestToUnownedServer(t *testing.T) {
	workspace := setTestWorkspace(t)
	file := filepath.Join(workspace, "a.go")
	rt := &fakeRuntime{packages: []fakePackage{
		newFakePackage("example/a", []string{file}, []testBlock{{0, 1, 1, false}, {0, 2, 2, false}, {0, 3, 3, false}}, false),
	}}
	c, sent := startTestCollector(t, rt)

	w := OpenLineCoverageWindow()
	testSlot := rt.slot
	rt.probe(0, 0)
	forget := c.Sent(11)

	// The handler's goroutine belongs to a server that TestMain started.
	rt.slot = 0
	restore := c.Received(11)
	assert.Equal(t, testSlot, rt.slot)
	rt.probe(0, 1)
	restore()
	assert.EqualValues(t, 0, rt.slot)
	rt.probe(0, 2) // the server's own work, outside any request

	// An unknown span ID, or one without a window, moves nothing.
	c.Received(12)()
	assert.EqualValues(t, 0, rt.slot)

	rt.slot = testSlot
	forget()
	w.Close(1, 1)
	assert.Empty(t, c.requests)

	c.stop()
	records := (*sent)[0]
	require.Len(t, records, 2)
	assert.Equal(t, map[string][]int32{"a.go": {1, 2}}, recordLines(t, records[0]))
	assert.Equal(t, "unowned", records[1]["shared"])
	assert.Equal(t, map[string][]int32{"a.go": {3}}, recordLines(t, records[1]))
}

func TestLineCoverageRequestFromSubtestToParentServer(t *testing.T) {
	workspace := setTestWorkspace(t)
	file := filepath.Join(workspace, "a.go")
	rt := &fakeRuntime{packages: []fakePackage{
		newFakePackage("example/a", []string{file}, []testBlock{{0, 1, 1, false}, {0, 2, 2, false}, {0, 3, 3, false}}, false),
	}}
	c, sent := startTestCollector(t, rt)

	parent := OpenLineCoverageWindow()
	parentSlot := rt.slot
	rt.startGoroutine() // the parent starts the server
	child := OpenLineCoverageWindow()
	childSlot := rt.slot
	forget := c.Sent(21)

	rt.slot = parentSlot // the server's goroutine
	restore := c.Received(21)
	assert.Equal(t, childSlot, rt.slot)
	rt.probe(0, 2)
	restore()
	assert.Equal(t, parentSlot, rt.slot, "back to the parent, whose window is open")
	rt.probe(0, 0)

	rt.slot = childSlot
	forget()
	child.Close(1, 2)

	// After the subtest's window closed, its requests move nothing.
	c.requests = map[uint64]sentRequest{21: {slot: childSlot, window: child}}
	rt.slot = parentSlot
	c.Received(21)()
	assert.Equal(t, parentSlot, rt.slot)

	parent.Close(1, 1)
	c.stop()
	records := (*sent)[0]
	require.GreaterOrEqual(t, len(records), 2)
	assert.EqualValues(t, 2, records[0]["span_id"])
	assert.Equal(t, map[string][]int32{"a.go": {3}}, recordLines(t, records[0]))
	assert.EqualValues(t, 1, records[1]["span_id"])
	assert.Equal(t, map[string][]int32{"a.go": {1}}, recordLines(t, records[1]))
}

func TestLineCoverageRequestOutlivesServerOwner(t *testing.T) {
	workspace := setTestWorkspace(t)
	file := filepath.Join(workspace, "a.go")
	rt := &fakeRuntime{packages: []fakePackage{
		newFakePackage("example/a", []string{file}, []testBlock{{0, 1, 1, false}}, false),
	}}
	c, _ := startTestCollector(t, rt)

	// Test B started the server; test A sends it a request.
	b := OpenLineCoverageWindow()
	bSlot := rt.slot
	rt.startGoroutine()
	rt.slot = 0
	a := OpenLineCoverageWindow()
	aSlot := rt.slot
	forget := c.Sent(31)

	rt.slot = bSlot // the server's goroutine
	restore := c.Received(31)
	assert.Equal(t, aSlot, rt.slot)

	// B ends while the handler runs.
	rt.slot = bSlot
	b.Close(1, 1)

	rt.slot = aSlot // the handler again
	restore()
	assert.EqualValues(t, 0, rt.slot, "B's slot is no longer B's")

	rt.slot = aSlot
	forget()
	a.Close(1, 2)
}
