// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"bytes"
	"encoding/binary"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	civisibilityutils "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
)

// testBlock is a block of a fake instrumented package.
type testBlock struct {
	file       int
	start, end int
	isInit     bool
}

// encodeTestMeta encodes a block table the way orchestrion's probe pass does.
func encodeTestMeta(files []string, blocks []testBlock) string {
	buf := binary.AppendUvarint(nil, lineCoverageMetaVersion)
	buf = binary.AppendUvarint(buf, uint64(len(files)))
	for _, f := range files {
		buf = binary.AppendUvarint(buf, uint64(len(f)))
		buf = append(buf, f...)
	}
	buf = binary.AppendUvarint(buf, uint64(len(blocks)))
	for _, b := range blocks {
		buf = binary.AppendUvarint(buf, uint64(b.file))
		buf = binary.AppendUvarint(buf, uint64(b.start))
		buf = binary.AppendUvarint(buf, uint64(b.end-b.start))
		var flags byte
		if b.isInit {
			flags |= lineCoverageBlockFlagInit
		}
		buf = append(buf, flags)
	}
	return string(buf)
}

// fakePackage is the counter array and block table of a fake instrumented
// package.
type fakePackage struct {
	counters  []byte
	numBlocks int
	rowSize   int
	path      string
	meta      string
}

// fakeRuntime stands in for the runtime accessors, for a single goroutine
// and a number of other goroutines per slot that retarget moves.
type fakeRuntime struct {
	packages []fakePackage
	slot     uint32
	refs     [lineCoverageSlotCount]int32
	others   map[uint32]int32
	running  bool // retarget reports a running goroutine
	sticky   bool // retarget moves nothing
}

func newFakePackage(path string, files []string, blocks []testBlock, misalign bool) fakePackage {
	rowSize := (len(blocks) + 63) / 64 * 64
	buf := make([]byte, lineCoverageSlotCount*rowSize+wordSize)
	counters := buf[:lineCoverageSlotCount*rowSize]
	if misalign {
		counters = buf[1 : 1+lineCoverageSlotCount*rowSize]
	}
	return fakePackage{counters: counters, numBlocks: len(blocks), rowSize: rowSize, path: path, meta: encodeTestMeta(files, blocks)}
}

func installFakeRuntime(t *testing.T, rt *fakeRuntime) {
	t.Helper()
	if rt.others == nil {
		rt.others = map[uint32]int32{}
	}
	savedSlot, savedSetSlot, savedRefs, savedRetarget, savedPackage := runtimeLineCoverageSlot,
		runtimeLineCoverageSetSlot, runtimeLineCoverageSlotRefs, runtimeLineCoverageRetarget, runtimeLineCoveragePackage
	runtimeLineCoverageSlot = func() uint32 { return rt.slot }
	runtimeLineCoverageSetSlot = func(slot uint32) uint32 {
		old := rt.slot
		if old != 0 {
			rt.refs[old]--
		}
		if slot != 0 {
			rt.refs[slot]++
		}
		rt.slot = slot
		return old
	}
	runtimeLineCoverageSlotRefs = func(slot uint32) int32 { return rt.refs[slot] }
	runtimeLineCoverageRetarget = func(from uint32) (int, bool) {
		if rt.sticky {
			return 0, rt.running
		}
		moved := rt.others[from]
		rt.refs[from] -= moved
		delete(rt.others, from)
		return int(moved), rt.running
	}
	runtimeLineCoveragePackage = func(i int) (*byte, int, int, string, string, bool) {
		if i >= len(rt.packages) {
			return nil, 0, 0, "", "", false
		}
		p := rt.packages[i]
		return &p.counters[0], p.numBlocks, p.rowSize, p.path, p.meta, true
	}
	t.Cleanup(func() {
		runtimeLineCoverageSlot, runtimeLineCoverageSetSlot, runtimeLineCoverageSlotRefs,
			runtimeLineCoverageRetarget, runtimeLineCoveragePackage = savedSlot, savedSetSlot, savedRefs, savedRetarget, savedPackage
		lineCoverage.Store(nil)
	})
}

// probe marks block of package pkg in the row of the current slot.
func (rt *fakeRuntime) probe(pkg, block int) {
	p := rt.packages[pkg]
	p.counters[int(rt.slot)*p.rowSize+block] = 1
}

// startGoroutine simulates a goroutine started by the current one.
func (rt *fakeRuntime) startGoroutine() {
	if rt.slot != 0 {
		rt.others[rt.slot]++
		rt.refs[rt.slot]++
	}
}

// startTestCollector starts a collector on rt, with a writer that keeps
// what it sends.
func startTestCollector(t *testing.T, rt *fakeRuntime) (*lineCoverageCollector, *[][]map[string]any) {
	t.Helper()
	installFakeRuntime(t, rt)
	c := newLineCoverageCollector(42)
	require.NotNil(t, c)
	sent := &[][]map[string]any{}
	c.writer = newCoverageWriter()
	c.writer.client = &MockClient{SendCoveragePayloadFunc: func(r io.Reader) error {
		*sent = append(*sent, decodeTestPayload(t, r))
		return nil
	}}
	lineCoverage.Store(c)
	return c, sent
}

func decodeTestPayload(t *testing.T, r io.Reader) []map[string]any {
	t.Helper()
	var payload ciTestCovPayload
	require.NoError(t, msgp.Decode(r, &payload))
	assert.EqualValues(t, 2, payload.Version)
	value, _, err := msgp.ReadIntfBytes(payload.Coverages)
	require.NoError(t, err)
	var records []map[string]any
	for _, v := range value.([]any) {
		records = append(records, v.(map[string]any))
	}
	return records
}

// recordLines returns the lines of each file of a decoded record.
func recordLines(t *testing.T, record map[string]any) map[string][]int32 {
	t.Helper()
	lines := map[string][]int32{}
	for _, f := range record["files"].([]any) {
		file := f.(map[string]any)
		lines[file["filename"].(string)] = bitmapLines(file["bitmap"].([]byte))
	}
	return lines
}

func setTestWorkspace(t *testing.T) string {
	t.Helper()
	civisibilityutils.ResetCITags()
	workspace := t.TempDir()
	civisibilityutils.AddCITagsMap(map[string]string{constants.CIWorkspacePath: workspace})
	t.Cleanup(civisibilityutils.ResetCITags)
	return workspace
}

func TestDecodeLineCoverageMeta(t *testing.T) {
	files := []string{"/repo/a.go", "/repo/b.go"}
	blocks := []testBlock{{0, 3, 5, false}, {1, 10, 10, true}, {0, 0, 0, false}}
	var names []string
	decoded, err := decodeLineCoverageMeta(encodeTestMeta(files, blocks), len(blocks), func(name string) int32 {
		names = append(names, name)
		return int32(len(names) - 1)
	})
	require.NoError(t, err)
	assert.Equal(t, files, names)
	assert.Equal(t, []lineCoverageBlock{
		{file: 0, startLine: 3, endLine: 5},
		{file: 1, startLine: 10, endLine: 10, isInit: true},
		{file: 0, startLine: 1, endLine: 0},
	}, decoded)

	meta := encodeTestMeta(files, blocks)
	for name, tc := range map[string]struct {
		meta      string
		numBlocks int
	}{
		"truncated":       {meta[:len(meta)-1], len(blocks)},
		"trailing bytes":  {meta + "x", len(blocks)},
		"block count":     {meta, len(blocks) + 1},
		"version":         {"\x02" + meta[1:], len(blocks)},
		"file index":      {encodeTestMeta(files[:1], blocks[1:2]), 1},
		"empty":           {"", 0},
		"varint overflow": {"\x01\xff\xff\xff\xff\xff\xff\xff\xff\xff\x7f", 0},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeLineCoverageMeta(tc.meta, tc.numBlocks, func(string) int32 { return 0 })
			assert.Error(t, err)
		})
	}
}

func TestLineCoverageWindowRecord(t *testing.T) {
	workspace := setTestWorkspace(t)
	calc := filepath.Join(workspace, "calc", "calc.go")
	calcTest := filepath.Join(workspace, "calc", "calc_test.go")
	util := filepath.Join(workspace, "util", "util.go")
	rt := &fakeRuntime{packages: []fakePackage{
		newFakePackage("example/util", []string{util}, []testBlock{
			{0, 3, 4, true}, // init
			{0, 7, 9, false},
		}, false),
		newFakePackage("example/calc", []string{calc, calcTest}, []testBlock{
			{0, 5, 6, true}, // init
			{0, 10, 12, false},
			{0, 14, 14, false},
			{1, 20, 22, false}, // the test
			{0, 30, 31, false}, // run before the tests, by TestMain
		}, false),
	}}
	// Package initialization and TestMain run in slot 0.
	rt.probe(0, 0)
	rt.probe(1, 0)
	rt.probe(1, 4)

	c, sent := startTestCollector(t, rt)
	assert.Equal(t, []int32{3, 4}, c.files[0].initLines)
	assert.Equal(t, []int32{5, 6}, c.files[1].initLines)
	assert.Equal(t, "calc/calc.go", c.files[1].name)

	w := OpenLineCoverageWindow()
	require.NotNil(t, w)
	assert.EqualValues(t, 1, rt.slot)
	rt.probe(1, 3)
	rt.probe(1, 1)
	w.Close(7, 8)
	assert.EqualValues(t, 0, rt.slot)

	// A second test reuses the slot, and gets only its own blocks.
	w = OpenLineCoverageWindow()
	assert.EqualValues(t, 1, rt.slot)
	rt.probe(1, 2)
	rt.probe(0, 1)
	w.Close(7, 9)

	c.stop()
	require.Len(t, *sent, 1)
	records := (*sent)[0]
	require.Len(t, records, 4)

	assert.EqualValues(t, 42, records[0]["test_session_id"])
	assert.EqualValues(t, 7, records[0]["test_suite_id"])
	assert.EqualValues(t, 8, records[0]["span_id"])
	assert.Equal(t, map[string][]int32{
		"calc/calc.go":      {5, 6, 10, 11, 12},
		"calc/calc_test.go": {20, 21, 22},
	}, recordLines(t, records[0]))

	assert.EqualValues(t, 9, records[1]["span_id"])
	assert.Equal(t, map[string][]int32{
		"calc/calc.go": {5, 6, 14},
		"util/util.go": {3, 4, 7, 8, 9},
	}, recordLines(t, records[1]))

	assert.Equal(t, "init", records[2]["shared"])
	assert.NotContains(t, records[2], "span_id")
	assert.Equal(t, map[string][]int32{
		"calc/calc.go": {5, 6},
		"util/util.go": {3, 4},
	}, recordLines(t, records[2]))

	assert.Equal(t, "unowned", records[3]["shared"])
	assert.Equal(t, map[string][]int32{"calc/calc.go": {30, 31}}, recordLines(t, records[3]))
	assert.Nil(t, lineCoverage.Load())
}

func TestLineCoverageSubtestWindows(t *testing.T) {
	workspace := setTestWorkspace(t)
	file := filepath.Join(workspace, "a.go")
	rt := &fakeRuntime{packages: []fakePackage{
		newFakePackage("example/a", []string{file}, []testBlock{{0, 1, 1, false}, {0, 2, 2, false}, {0, 3, 3, false}}, false),
	}}
	c, sent := startTestCollector(t, rt)

	parent := OpenLineCoverageWindow()
	rt.probe(0, 0)
	// t.Run starts the subtest's goroutine, which first carries the parent's slot.
	child := OpenLineCoverageWindow()
	assert.EqualValues(t, 2, rt.slot)
	rt.probe(0, 1)
	child.Close(1, 2)
	rt.slot = 1
	rt.refs[1]++
	rt.probe(0, 2)
	parent.Close(1, 1)

	c.stop()
	records := (*sent)[0]
	require.Len(t, records, 2)
	assert.Equal(t, map[string][]int32{"a.go": {2}}, recordLines(t, records[0]))
	assert.Equal(t, map[string][]int32{"a.go": {1, 3}}, recordLines(t, records[1]))
}

func TestLineCoverageDetach(t *testing.T) {
	workspace := setTestWorkspace(t)
	file := filepath.Join(workspace, "a.go")
	rt := &fakeRuntime{packages: []fakePackage{
		newFakePackage("example/a", []string{file}, []testBlock{{0, 1, 1, false}, {0, 2, 2, false}}, false),
	}}
	c, sent := startTestCollector(t, rt)

	// A goroutine that outlives its test, and runs when the window closes.
	w := OpenLineCoverageWindow()
	rt.startGoroutine()
	rt.running = true
	rt.probe(0, 0)
	w.Close(1, 1)
	assert.Empty(t, rt.others)
	assert.Equal(t, []uint32{1}, c.dirty)

	// The next window takes a clean slot; the dirty one is reused last.
	w = OpenLineCoverageWindow()
	assert.EqualValues(t, 2, rt.slot)
	w.Close(1, 2)

	// A late store of the goroutine of the first window goes to no test.
	rt.packages[0].counters[1*rt.packages[0].rowSize+1] = 1

	// Goroutines that keep taking a slot poison it.
	rt.running = false
	rt.sticky = true
	w = OpenLineCoverageWindow()
	slot := rt.slot
	rt.startGoroutine()
	w.Close(1, 3)
	assert.Equal(t, []uint32{slot}, c.poisoned)
	assert.NotContains(t, c.free, slot)

	c.stop()
	records := (*sent)[0]
	require.Len(t, records, 4)
	assert.Equal(t, map[string][]int32{"a.go": {1}}, recordLines(t, records[0]))
	assert.Equal(t, "unowned", records[3]["shared"])
	assert.Equal(t, map[string][]int32{"a.go": {2}}, recordLines(t, records[3]))
}

func TestLineCoverageDirtySlotReuse(t *testing.T) {
	workspace := setTestWorkspace(t)
	file := filepath.Join(workspace, "a.go")
	rt := &fakeRuntime{packages: []fakePackage{
		newFakePackage("example/a", []string{file}, []testBlock{{0, 1, 1, false}, {0, 2, 2, false}}, false),
	}}
	c, sent := startTestCollector(t, rt)
	c.free = c.free[len(c.free)-1:] // only slot 1 is clean

	w := OpenLineCoverageWindow()
	rt.startGoroutine()
	rt.running = true
	w.Close(1, 1)
	rt.packages[0].counters[1*rt.packages[0].rowSize+1] = 1 // late store

	// With no clean slot left, the dirty one is reused, after its late stores
	// went to no test.
	rt.running = false
	w = OpenLineCoverageWindow()
	assert.EqualValues(t, 1, rt.slot)
	rt.probe(0, 0)
	w.Close(1, 2)

	// With no slot at all, a window sends no record.
	c.dirty, c.free = nil, nil
	w = OpenLineCoverageWindow()
	assert.EqualValues(t, 0, rt.slot)
	rt.probe(0, 0)
	w.Close(1, 3)
	assert.Equal(t, 1, c.missed)

	c.stop()
	records := (*sent)[0]
	require.Len(t, records, 3)
	assert.Equal(t, map[string][]int32{"a.go": {1}}, recordLines(t, records[1]))
	assert.Equal(t, map[string][]int32{"a.go": {1, 2}}, recordLines(t, records[2]))
}

func TestLineCoveragePauseResume(t *testing.T) {
	workspace := setTestWorkspace(t)
	file := filepath.Join(workspace, "a.go")
	rt := &fakeRuntime{packages: []fakePackage{
		newFakePackage("example/a", []string{file}, []testBlock{{0, 1, 1, false}, {0, 2, 2, false}}, false),
	}}
	c, sent := startTestCollector(t, rt)

	w := OpenLineCoverageWindow()
	rt.probe(0, 0)
	resume := PauseLineCoverageWindow()
	assert.EqualValues(t, 0, rt.slot)
	assert.Equal(t, []uint32{1}, c.free[len(c.free)-1:])

	// Another test takes the slot while the first one waits.
	other := OpenLineCoverageWindow()
	assert.EqualValues(t, 1, rt.slot)
	other.Close(1, 2)

	resume()
	assert.EqualValues(t, 1, rt.slot)
	rt.probe(0, 1)
	w.Close(1, 1)

	// A window whose slot other goroutines carry keeps it.
	w = OpenLineCoverageWindow()
	rt.startGoroutine()
	resume = PauseLineCoverageWindow()
	assert.EqualValues(t, 1, rt.slot)
	resume()
	assert.EqualValues(t, 1, rt.slot)
	w.Close(1, 3)

	c.stop()
	records := (*sent)[0]
	require.Len(t, records, 3)
	assert.EqualValues(t, 2, records[0]["span_id"])
	assert.Equal(t, map[string][]int32{"a.go": {1, 2}}, recordLines(t, records[1]))
}

func TestLineCoverageUnalignedCounters(t *testing.T) {
	workspace := setTestWorkspace(t)
	file := filepath.Join(workspace, "a.go")
	blocks := make([]testBlock, 70)
	for i := range blocks {
		blocks[i] = testBlock{0, i + 1, i + 1, false}
	}
	rt := &fakeRuntime{packages: []fakePackage{newFakePackage("example/a", []string{file}, blocks, true)}}
	c, sent := startTestCollector(t, rt)
	require.False(t, c.packages[0].aligned)

	w := OpenLineCoverageWindow()
	rt.probe(0, 0)
	rt.probe(0, 69)
	w.Close(1, 1)
	w = OpenLineCoverageWindow()
	rt.probe(0, 8)
	w.Close(1, 2)

	c.stop()
	records := (*sent)[0]
	assert.Equal(t, map[string][]int32{"a.go": {1, 70}}, recordLines(t, records[0]))
	assert.Equal(t, map[string][]int32{"a.go": {9}}, recordLines(t, records[1]))
}

func TestLineCoverageOff(t *testing.T) {
	installFakeRuntime(t, &fakeRuntime{})
	runtimeLineCoverageRetarget = nil
	assert.False(t, StartLineCoverage(1))
	assert.Nil(t, OpenLineCoverageWindow())
	PauseLineCoverageWindow()()
	var w *LineCoverageWindow
	w.Close(1, 1)
}

func TestLineCoverageSharedRecordEncoding(t *testing.T) {
	record := &ciTestCoverageSharedData{
		SessionID: 3,
		Part:      sharedPartUnowned,
		Files:     []*ciTestCoverageFile{{FileName: "a.go", Bitmap: []byte{0x80}}},
	}
	var buf bytes.Buffer
	require.NoError(t, msgp.Encode(&buf, record))
	assert.LessOrEqual(t, buf.Len(), record.Msgsize())
	value, _, err := msgp.ReadIntfBytes(buf.Bytes())
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"test_session_id": int64(3), // msgp decodes small unsigned integers as int64
		"shared":          "unowned",
		"files":           []any{map[string]any{"filename": "a.go", "bitmap": []byte{0x80}}},
	}, value)
}

func TestBitmapLines(t *testing.T) {
	bitmap := make([]byte, 3)
	for _, line := range []int32{1, 8, 9, 24, 0, 25} {
		setLine(bitmap, line)
	}
	assert.Equal(t, []byte{0x81, 0x80, 0x01}, bitmap)
	assert.Equal(t, []int32{1, 8, 9, 24}, bitmapLines(bitmap))
}
