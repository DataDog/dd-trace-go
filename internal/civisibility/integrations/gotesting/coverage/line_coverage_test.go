// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"bytes"
	"encoding/binary"
	"io"
	"maps"
	"math"
	"path/filepath"
	"slices"
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

// testLines are the lines a block of a fake package credits besides its range.
// With noRange, the block has no range.
type testLines struct {
	lines   []int
	noRange bool
}

// encodeTestMeta encodes a block table the way orchestrion's probe pass does.
func encodeTestMeta(files []string, blocks []testBlock) string {
	return encodeTestMetaLines(files, blocks, nil)
}

// encodeTestMetaLines encodes a block table whose blocks credit the lines of
// extras, by block index.
func encodeTestMetaLines(files []string, blocks []testBlock, extras map[int]testLines) string {
	return encodeTestMetaSized(files, blocks, extras, 1)
}

// encodeTestMetaSized encodes a block table with counters of counterSize bytes.
func encodeTestMetaSized(files []string, blocks []testBlock, extras map[int]testLines, counterSize int) string {
	return encodeTestMetaInit(files, blocks, extras, counterSize, nil)
}

// encodeTestMetaInit encodes a block table with the init lines of initLines,
// by file index of the table.
func encodeTestMetaInit(files []string, blocks []testBlock, extras map[int]testLines, counterSize int, initLines map[int][]int) string {
	return encodeTestMetaTable(files, blocks, extras, counterSize, initLines, nil)
}

// testFileLines are the lines of a file in the line table of a fake package.
type testFileLines struct {
	code, varInit []int
}

// testLineTable is the line table of a fake package: own has the lines of its
// files, by file index of the block table, and other those of other packages'
// files, by name.
type testLineTable struct {
	own   map[int]testFileLines
	other map[string]testFileLines
}

// encodeTestMetaTable encodes a block table with a line table, or without one
// when table is nil.
func encodeTestMetaTable(files []string, blocks []testBlock, extras map[int]testLines, counterSize int, initLines map[int][]int, table *testLineTable) string {
	buf := binary.AppendUvarint(nil, lineCoverageMetaVersion)
	buf = binary.AppendUvarint(buf, uint64(counterSize))
	buf = binary.AppendUvarint(buf, uint64(len(files)))
	for _, f := range files {
		buf = binary.AppendUvarint(buf, uint64(len(f)))
		buf = append(buf, f...)
	}
	buf = binary.AppendUvarint(buf, uint64(len(blocks)))
	for i, b := range blocks {
		buf = binary.AppendUvarint(buf, uint64(b.file))
		buf = binary.AppendUvarint(buf, uint64(b.start))
		buf = binary.AppendUvarint(buf, uint64(b.end-b.start))
		var flags byte
		if b.isInit {
			flags |= lineCoverageBlockFlagInit
		}
		extra := extras[i]
		if len(extra.lines) > 0 {
			flags |= lineCoverageBlockFlagLines
		}
		if extra.noRange {
			flags |= lineCoverageBlockFlagNoRange
		}
		buf = append(buf, flags)
		if len(extra.lines) > 0 {
			buf = binary.AppendUvarint(buf, uint64(len(extra.lines)))
			for _, line := range extra.lines {
				buf = binary.AppendUvarint(buf, uint64(line))
			}
		}
	}
	buf = binary.AppendUvarint(buf, uint64(len(initLines)))
	for _, file := range slices.Sorted(maps.Keys(initLines)) {
		buf = binary.AppendUvarint(buf, uint64(file))
		buf = binary.AppendUvarint(buf, uint64(len(initLines[file])))
		for _, line := range initLines[file] {
			buf = binary.AppendUvarint(buf, uint64(line))
		}
	}
	if table == nil {
		return string(binary.AppendUvarint(buf, 0))
	}
	buf = binary.AppendUvarint(buf, 1)
	buf = binary.AppendUvarint(buf, uint64(len(table.own)))
	for _, file := range slices.Sorted(maps.Keys(table.own)) {
		buf = binary.AppendUvarint(buf, uint64(file))
		buf = appendTestFileLines(buf, table.own[file])
	}
	buf = binary.AppendUvarint(buf, uint64(len(table.other)))
	for _, name := range slices.Sorted(maps.Keys(table.other)) {
		buf = binary.AppendUvarint(buf, uint64(len(name)))
		buf = append(buf, name...)
		buf = appendTestFileLines(buf, table.other[name])
	}
	return string(buf)
}

func appendTestFileLines(buf []byte, lines testFileLines) []byte {
	for _, ls := range [][]int{lines.code, lines.varInit} {
		buf = binary.AppendUvarint(buf, uint64(len(ls)))
		prev := 0
		for _, line := range ls {
			buf = binary.AppendUvarint(buf, uint64(line-prev))
			prev = line
		}
	}
	return buf
}

// fakePackage is the counter array and block table of a fake instrumented
// package.
type fakePackage struct {
	counters    []byte
	numBlocks   int
	rowSize     int // in bytes
	counterSize int
	path        string
	meta        string
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
	// lines is the line table of the binary; without it, the runtime has no
	// line table accessor.
	lines []fakeLine
}

// fakeLine is a run of instructions of the line table.
type fakeLine struct {
	funcName, file string
	line           int32
	inlined        bool
}

func newFakePackage(path string, files []string, blocks []testBlock, misalign bool) fakePackage {
	return newFakePackageLines(path, files, blocks, nil, misalign)
}

func newFakePackageLines(path string, files []string, blocks []testBlock, extras map[int]testLines, misalign bool) fakePackage {
	return newFakePackageSized(path, files, blocks, extras, misalign, 1)
}

// newFakePackageSized returns a fake package with counters of counterSize
// bytes.
func newFakePackageSized(path string, files []string, blocks []testBlock, extras map[int]testLines, misalign bool, counterSize int) fakePackage {
	rowSize := (len(blocks)*counterSize + 63) / 64 * 64
	buf := make([]byte, lineCoverageSlotCount*rowSize+wordSize)
	counters := buf[:lineCoverageSlotCount*rowSize]
	if misalign {
		counters = buf[1 : 1+lineCoverageSlotCount*rowSize]
	}
	return fakePackage{
		counters:    counters,
		numBlocks:   len(blocks),
		rowSize:     rowSize,
		counterSize: counterSize,
		path:        path,
		meta:        encodeTestMetaSized(files, blocks, extras, counterSize),
	}
}

func installFakeRuntime(t *testing.T, rt *fakeRuntime) {
	t.Helper()
	if rt.others == nil {
		rt.others = map[uint32]int32{}
	}
	savedSlot, savedSetSlot, savedRefs, savedRetarget, savedPackage := runtimeLineCoverageSlot,
		runtimeLineCoverageSetSlot, runtimeLineCoverageSlotRefs, runtimeLineCoverageRetarget, runtimeLineCoveragePackage
	savedLines := runtimeLineCoverageLines
	runtimeLineCoverageLines = nil
	if rt.lines != nil {
		runtimeLineCoverageLines = func(wantFile func(string) bool, line func(string, string, int32, bool)) {
			for _, l := range rt.lines {
				if wantFile(l.file) {
					line(l.funcName, l.file, l.line, l.inlined)
				}
			}
		}
	}
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
		var counters *byte
		if len(p.counters) > 0 {
			counters = &p.counters[0]
		}
		return counters, p.numBlocks, p.rowSize, p.path, p.meta, true
	}
	t.Cleanup(func() {
		runtimeLineCoverageSlot, runtimeLineCoverageSetSlot, runtimeLineCoverageSlotRefs,
			runtimeLineCoverageRetarget, runtimeLineCoveragePackage = savedSlot, savedSetSlot, savedRefs, savedRetarget, savedPackage
		runtimeLineCoverageLines = savedLines
		lineCoverage.Store(nil)
	})
}

// probe marks block of package pkg in the row of the current slot.
func (rt *fakeRuntime) probe(pkg, block int) {
	rt.packages[pkg].store(rt.slot, block)
}

// store marks block in the row of slot.
func (p fakePackage) store(slot uint32, block int) {
	if p.counterSize == 4 {
		binary.NativeEndian.PutUint32(p.counters[int(slot)*p.rowSize+4*block:], 1)
		return
	}
	p.counters[int(slot)*p.rowSize+block] = 1
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
	table, err := decodeLineCoverageMeta(encodeTestMeta(files, blocks), len(blocks), func(name string) int32 {
		names = append(names, name)
		return int32(len(names) - 1)
	})
	require.NoError(t, err)
	assert.Equal(t, 1, table.counterSize)
	assert.Equal(t, files, names)
	assert.Equal(t, []lineCoverageBlock{
		{file: 0, startLine: 3, endLine: 5},
		{file: 1, startLine: 10, endLine: 10, isInit: true},
		{file: 0, startLine: 1, endLine: 0},
	}, table.blocks)
	assert.Nil(t, table.initLines)
	assert.Equal(t, []int32{0, 1}, table.files)
	assert.Nil(t, table.lines)

	table, err = decodeLineCoverageMeta(encodeTestMetaSized(files, blocks, nil, 4), len(blocks), func(string) int32 { return 0 })
	require.NoError(t, err)
	assert.Equal(t, 4, table.counterSize)

	// The tables of earlier versions have no line table. Versions before 4 have
	// no init lines either, and versions 1 and 2 no counter size: their counters
	// are bytes. Version 1 tables have no credited lines.
	meta := encodeTestMeta(files, blocks)
	withoutTable := meta[:len(meta)-1]
	withoutInit := withoutTable[:len(withoutTable)-1]
	for _, old := range []string{"\x01" + withoutInit[2:], "\x02" + withoutInit[2:], "\x03" + withoutInit[1:], "\x04" + withoutTable[1:]} {
		table, err := decodeLineCoverageMeta(old, len(blocks), func(string) int32 { return 0 })
		require.NoError(t, err)
		assert.Len(t, table.blocks, len(blocks))
		assert.Equal(t, 1, table.counterSize)
		assert.Nil(t, table.lines)
	}

	// A line table has the code lines of the package's files, and those of other
	// packages' files.
	index := map[string]int32{"/repo/a.go": 10, "/repo/b.go": 11, "/other/c.go": 20}
	withTable := encodeTestMetaTable(files, blocks, nil, 1, nil, &testLineTable{
		own:   map[int]testFileLines{1: {code: []int{3, 4, 10}, varInit: []int{4}}},
		other: map[string]testFileLines{"/other/c.go": {code: []int{7}}},
	})
	table, err = decodeLineCoverageMeta(withTable, len(blocks), func(name string) int32 { return index[name] })
	require.NoError(t, err)
	assert.Equal(t, []int32{10, 11}, table.files)
	assert.Equal(t, &lineCoverageLines{
		code:    map[int32][]int32{11: {3, 4, 10}, 20: {7}},
		varInit: map[int32][]int32{11: {4}},
	}, table.lines)

	// A package without code has an empty line table.
	table, err = decodeLineCoverageMeta(encodeTestMetaTable(files, blocks, nil, 1, nil, &testLineTable{}), len(blocks), func(string) int32 { return 0 })
	require.NoError(t, err)
	assert.Equal(t, &lineCoverageLines{code: map[int32][]int32{}, varInit: map[int32][]int32{}}, table.lines)
	// tableMeta returns meta with the line table encoded as table.
	tableMeta := func(table string) string { return withoutTable + table }

	withLines := encodeTestMetaLines(files, blocks, map[int]testLines{
		0: {lines: []int{2}},
		2: {lines: []int{7, 9}, noRange: true},
	})
	table, err = decodeLineCoverageMeta(withLines, len(blocks), func(string) int32 { return 0 })
	require.NoError(t, err)
	assert.Equal(t, []lineCoverageBlock{
		{file: 0, startLine: 3, endLine: 5, lines: []int32{2}},
		{file: 0, startLine: 10, endLine: 10, isInit: true},
		{file: 0, startLine: 1, endLine: 0, lines: []int32{7, 9}},
	}, table.blocks)

	indexOf := func(name string) int32 { return int32(slices.Index(files, name)) + 10 }
	withInit := encodeTestMetaInit(files, blocks, nil, 1, map[int][]int{1: {4, 9}})
	table, err = decodeLineCoverageMeta(withInit, len(blocks), indexOf)
	require.NoError(t, err)
	assert.Equal(t, map[int32][]int32{11: {4, 9}}, table.initLines)

	// A package without blocks lists its files and init lines only.
	table, err = decodeLineCoverageMeta(encodeTestMetaInit(files[:1], nil, nil, 1, map[int][]int{0: {3}}), 0, indexOf)
	require.NoError(t, err)
	assert.Empty(t, table.blocks)
	assert.Equal(t, map[int32][]int32{10: {3}}, table.initLines)

	zeroLine := encodeTestMetaLines(files, blocks[:1], map[int]testLines{0: {lines: []int{0}}})
	for name, tc := range map[string]struct {
		meta      string
		numBlocks int
	}{
		"truncated":           {meta[:len(meta)-1], len(blocks)},
		"truncated lines":     {withLines[:len(withLines)-1], len(blocks)},
		"truncated init":      {withInit[:len(withInit)-2], len(blocks)},
		"truncated table":     {withTable[:len(withTable)-1], len(blocks)},
		"trailing bytes":      {meta + "x", len(blocks)},
		"block count":         {meta, len(blocks) + 1},
		"version":             {"\x06" + meta[1:], len(blocks)},
		"version zero":        {"\x00" + meta[1:], len(blocks)},
		"counter size":        {meta[:1] + "\x02" + meta[2:], len(blocks)},
		"file index":          {encodeTestMeta(files[:1], blocks[1:2]), 1},
		"line zero":           {zeroLine, 1},
		"line count":          {zeroLine[:len(zeroLine)-4] + "\x7f\x01", 1},
		"init file index":     {encodeTestMetaInit(files[:1], nil, nil, 1, map[int][]int{1: {3}}), 0},
		"init line zero":      {encodeTestMetaInit(files[:1], nil, nil, 1, map[int][]int{0: {0}}), 0},
		"init file count":     {encodeTestMetaInit(files[:1], nil, nil, 1, map[int][]int{0: {3}, 1: {4}}), 0},
		"table flag":          {tableMeta("\x02"), len(blocks)},
		"table file count":    {tableMeta("\x01\x03"), len(blocks)},
		"table file index":    {tableMeta("\x01\x01\x02\x00\x00\x00"), len(blocks)},
		"table other count":   {tableMeta("\x01\x00\x7f"), len(blocks)},
		"table line count":    {tableMeta("\x01\x01\x00\x7f"), len(blocks)},
		"table line repeated": {tableMeta("\x01\x01\x00\x02\x03\x00\x00\x00"), len(blocks)},
		"table line overflow": {tableMeta("\x01\x01\x00\x01" + string(binary.AppendUvarint(nil, math.MaxInt32+1)) + "\x00\x00"), len(blocks)},
		"empty":               {"", 0},
		"varint overflow":     {"\x01\xff\xff\xff\xff\xff\xff\xff\xff\xff\x7f", 0},
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

// TestLineCoverageWideCounters checks the uint32 counters of builds with the
// race detector.
func TestLineCoverageWideCounters(t *testing.T) {
	workspace := setTestWorkspace(t)
	file := filepath.Join(workspace, "a.go")
	blocks := []testBlock{{0, 1, 1, false}, {0, 2, 2, false}, {0, 3, 3, false}}
	short := newFakePackageSized("example/short", []string{file}, blocks, nil, false, 4)
	short.rowSize = 8 // fewer bytes than three uint32 counters
	rt := &fakeRuntime{packages: []fakePackage{
		newFakePackageSized("example/a", []string{file}, blocks, nil, false, 4),
		short,
	}}
	c, sent := startTestCollector(t, rt)
	require.Len(t, c.packages, 1)
	require.True(t, c.packages[0].wide)

	w := OpenLineCoverageWindow()
	slot := rt.slot
	rt.probe(0, 0)
	rt.probe(0, 2)
	w.Close(1, 1)
	p := rt.packages[0]
	assert.Equal(t, make([]byte, p.rowSize), p.counters[int(slot)*p.rowSize:int(slot+1)*p.rowSize], "rows are cleared")

	c.stop()
	records := (*sent)[0]
	require.NotEmpty(t, records)
	assert.Equal(t, map[string][]int32{"a.go": {1, 3}}, recordLines(t, records[0]))
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

func TestLineCoverageLineTable(t *testing.T) {
	workspace := setTestWorkspace(t)
	a := filepath.Join(workspace, "a.go")
	b := filepath.Join(workspace, "b.go")
	rt := &fakeRuntime{
		packages: []fakePackage{
			newFakePackageLines("example/a", []string{a, b}, []testBlock{
				{0, 3, 6, false},   // the body of a function whose header is on line 2
				{0, 7, 7, false},   // an end probe: no range, the closing brace
				{0, 8, 8, false},   // an end probe on a brace without code
				{0, 10, 11, true},  // an init function
				{1, 2, 4, false},   // a file the line table has no lines for
				{0, 30, 30, false}, // a block of a line without code
			}, map[int]testLines{
				0: {lines: []int{2}},
				1: {lines: []int{7}, noRange: true},
				2: {lines: []int{8}, noRange: true},
				4: {lines: []int{1}},
			}, false),
		},
		lines: []fakeLine{
			{"example/a.F", a, 2, false},
			{"example/a.F", a, 4, false},
			{"example/a.F", a, 6, true},
			{"example/a.F", a, 7, false},
			{"example/a.init.0", a, 10, false},
			{"example/a.init.0", a, 11, false},
			{"example/a.init", a, 20, false},
			{"example/a.init", a, 21, true}, // a function inlined into the initialisers
			{"example/a.map.init.0", a, 22, false},
			{"example/a.init", filepath.Join(workspace, "other.go"), 5, false},
		},
	}
	rt.probe(0, 3) // package initialization
	c, sent := startTestCollector(t, rt)
	assert.Equal(t, []int32{10, 11, 20, 22}, c.files[0].initLines)
	assert.EqualValues(t, 30, c.files[0].maxLine)
	assert.Nil(t, c.files[1].codeLines)
	require.Len(t, c.files, 2)

	w := OpenLineCoverageWindow()
	for _, block := range []int{0, 1, 2, 5} {
		rt.probe(0, block)
	}
	w.Close(1, 1)
	w = OpenLineCoverageWindow()
	rt.probe(0, 4)
	w.Close(1, 2)

	c.stop()
	records := (*sent)[0]
	require.Len(t, records, 3)
	assert.Equal(t, map[string][]int32{"a.go": {2, 4, 6, 7, 10, 11, 20, 22}}, recordLines(t, records[0]))
	assert.Equal(t, map[string][]int32{"b.go": {2, 3, 4}}, recordLines(t, records[1]))
	assert.Equal(t, "init", records[2]["shared"])
	assert.Equal(t, map[string][]int32{"a.go": {10, 11, 20, 22}}, recordLines(t, records[2]))
}

// TestLineCoverageInitLinesFromTables checks the init lines that block tables
// list: the header lines of function literals in package-level variable
// initialisers, where the line table maps code to them, and the files of a
// package without blocks, whose initialiser lines come from the line table.
func TestLineCoverageInitLinesFromTables(t *testing.T) {
	workspace := setTestWorkspace(t)
	a := filepath.Join(workspace, "a.go")
	pool := filepath.Join(workspace, "pool", "pool.go")
	blocks := []testBlock{
		{0, 3, 3, false}, // a function
		{0, 6, 6, false}, // the body of a closure built at package initialization
	}
	withClosures := newFakePackage("example/a", []string{a}, blocks, false)
	// Line 8 is the header of a closure the line table maps no code to.
	withClosures.meta = encodeTestMetaInit([]string{a}, blocks, nil, 1, map[int][]int{0: {5, 8}})
	rt := &fakeRuntime{
		packages: []fakePackage{
			withClosures,
			{path: "example/pool", meta: encodeTestMeta([]string{pool}, nil)},
		},
		lines: []fakeLine{
			{"example/a.F", a, 3, false},
			{"example/a.init.func1", a, 5, false},
			{"example/a.init.func1", a, 6, false},
			{"example/pool.init", pool, 4, false},
			{"example/pool.init", pool, 6, false},
		},
	}
	c, sent := startTestCollector(t, rt)
	require.Len(t, c.files, 2)
	require.Len(t, c.packages, 1)
	assert.Equal(t, []int32{5}, c.files[0].initLines)
	assert.Equal(t, []int32{4, 6}, c.files[1].initLines)

	w := OpenLineCoverageWindow()
	rt.probe(0, 1)
	w.Close(1, 1)

	c.stop()
	records := (*sent)[0]
	require.Len(t, records, 2)
	assert.Equal(t, map[string][]int32{"a.go": {5, 6}}, recordLines(t, records[0]))
	assert.Equal(t, "init", records[1]["shared"])
	assert.Equal(t, map[string][]int32{"a.go": {5}, "pool/pool.go": {4, 6}}, recordLines(t, records[1]))
}

// TestLineCoverageTableLines checks that the line tables of the block tables
// replace the line table of the binary for the files of their packages, and
// only for those.
func TestLineCoverageTableLines(t *testing.T) {
	workspace := setTestWorkspace(t)
	a := filepath.Join(workspace, "a.go")
	b := filepath.Join(workspace, "b.go")
	gen := filepath.Join(workspace, "gen", "gen.go")
	lib := filepath.Join(workspace, "lib", "lib.go")
	types := filepath.Join(workspace, "types", "types.go")
	blocksA := []testBlock{
		{0, 3, 6, false},   // the body of a function whose header is on line 2
		{0, 10, 11, true},  // an init function
		{1, 2, 4, false},   // a file without code in the line table
		{0, 25, 25, false}, // the body of a closure built at package initialization
	}
	pkgA := newFakePackage("example/a", []string{a, b}, blocksA, false)
	pkgA.meta = encodeTestMetaTable([]string{a, b}, blocksA, map[int]testLines{0: {lines: []int{2}}}, 1,
		map[int][]int{0: {24, 26}}, // closure headers; line 26 has no code
		&testLineTable{
			own: map[int]testFileLines{0: {code: []int{2, 4, 6, 10, 11, 20, 24, 25}, varInit: []int{20}}},
			// Generic functions of packages gen and lib that package a instantiates.
			other: map[string]testFileLines{gen: {code: []int{5}}, lib: {code: []int{3}}},
		})
	rt := &fakeRuntime{
		packages: []fakePackage{
			pkgA,
			// Without a line table: its lines come from the line table of the binary.
			newFakePackage("example/gen", []string{gen}, []testBlock{{0, 4, 7, false}}, false),
			// Without a line table, and without lines in that of the binary.
			newFakePackage("example/lib", []string{lib}, []testBlock{{0, 2, 4, false}}, false),
			{path: "example/types", meta: encodeTestMetaTable([]string{types}, nil, nil, 1, nil, &testLineTable{})},
		},
		lines: []fakeLine{
			// The binary has lines that the line tables of the packages have not.
			{"example/a.F", a, 3, false},
			{"example/a.G", a, 5, true},
			{"example/a.init", a, 21, false},
			{"example/a.init.func2", a, 26, false},
			{"example/a.F", b, 3, false},
			{"example/types.init", types, 3, false},
			{"example/gen.Map[...]", gen, 6, false},
			{"example/gen.init", gen, 9, false},
		},
	}
	rt.probe(0, 1) // package initialization
	c, sent := startTestCollector(t, rt)
	var names []string
	for _, f := range c.files {
		names = append(names, f.name)
	}
	require.Equal(t, []string{"a.go", "b.go", "gen/gen.go", "lib/lib.go", "types/types.go"}, names)
	assert.Equal(t, []int32{2, 4, 6, 10, 11, 20, 24, 25}, bitmapLines(c.files[0].codeLines))
	assert.Equal(t, []int32{10, 11, 20, 24}, c.files[0].initLines)
	assert.NotNil(t, c.files[1].codeLines)
	assert.Empty(t, bitmapLines(c.files[1].codeLines))
	assert.Equal(t, []int32{5, 6, 9}, bitmapLines(c.files[2].codeLines))
	assert.Equal(t, []int32{9}, c.files[2].initLines)
	// The lines of package a's generic instantiations are not all of lib.go's.
	assert.Nil(t, c.files[3].codeLines)
	assert.NotNil(t, c.files[4].codeLines)
	assert.Empty(t, c.files[4].initLines)

	w := OpenLineCoverageWindow()
	rt.probe(0, 0)
	rt.probe(0, 2)
	rt.probe(0, 3)
	rt.probe(1, 0)
	rt.probe(2, 0)
	w.Close(1, 1)

	c.stop()
	records := (*sent)[0]
	require.Len(t, records, 2)
	assert.Equal(t, map[string][]int32{
		"a.go":       {2, 4, 6, 10, 11, 20, 24, 25},
		"gen/gen.go": {5, 6, 9},
		"lib/lib.go": {2, 3, 4},
	}, recordLines(t, records[0]))
	assert.Equal(t, "init", records[1]["shared"])
	assert.Equal(t, map[string][]int32{"a.go": {10, 11, 20, 24}, "gen/gen.go": {9}}, recordLines(t, records[1]))
}

func TestIsPackageVarInitFunc(t *testing.T) {
	for name, want := range map[string]bool{
		"example.com/a.init":          true,
		"a.init":                      true,
		"gopkg.in/yaml%2ev3.init":     true,
		"example.com/a.map.init.0":    true,
		"example.com/a.map.init.12":   true,
		"example.com/a.init.0":        false,
		"example.com/a.init.0.func1":  false,
		"example.com/a.init.func1":    false,
		"example.com/a.T.init":        false,
		"example.com/a.(*T).init":     false,
		"example.com/a.map.init.":     false,
		"example.com/a.map.init.x":    false,
		"example.com/a.F":             false,
		".init":                       false,
		"gopkg.in/yaml.v3.init":       false,
		"example.com/a.T.map.init.0":  false,
		"example.com/a.map.init.0.f1": false,
	} {
		assert.Equal(t, want, isPackageVarInitFunc(name), name)
	}
}

func TestBitmapLines(t *testing.T) {
	bitmap := make([]byte, 3)
	for _, line := range []int32{1, 8, 9, 24, 0, 25} {
		setLine(bitmap, line)
	}
	assert.Equal(t, []byte{0x81, 0x80, 0x01}, bitmap)
	assert.Equal(t, []int32{1, 8, 9, 24}, bitmapLines(bitmap))
}
