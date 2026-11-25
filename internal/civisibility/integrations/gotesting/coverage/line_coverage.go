// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"cmp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/coveragepropagation"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	// lineCoverageSlotCount is the number of coverage slots in the runtime.
	// Slot 0 collects the work of no test; windows lease the others.
	lineCoverageSlotCount = 1024

	// maxDetachAttempts bounds the retargets of a slot that goroutines keep
	// taking while its window closes.
	maxDetachAttempts = 100

	// Parts of Shared: the lines a process ran outside the tests' own coverage.
	sharedPartInit    = "init"
	sharedPartUnowned = "unowned"

	wordSize = int(unsafe.Sizeof(uintptr(0)))
)

type (
	// lineCoverageCollector collects per-test line coverage from the probes that
	// orchestrion adds to repository code.
	lineCoverageCollector struct {
		sessionID uint64
		packages  []lineCoveragePackage // read-only once started
		files     []lineCoverageFile    // read-only once started
		writer    *coverageWriter

		mu       locking.Mutex
		free     []uint32 // clean slots, reused last in, first out
		dirty    []uint32 // slots that may still get stores of closed windows, oldest first
		poisoned []uint32 // slots that goroutines kept taking; never reused
		windows  [lineCoverageSlotCount]*LineCoverageWindow
		late     []blockRef // blocks that dirty slots got after their windows closed
		missed   int        // windows that ran without a slot for a while
		stopped  bool
		// requests are the in-flight requests that goroutines of open windows
		// sent, by the span ID they carry; see line_coverage_propagation.go.
		requests map[uint64]sentRequest
	}

	// lineCoveragePackage is the counter array of one instrumented package.
	lineCoveragePackage struct {
		counters unsafe.Pointer
		rowSize  int // in bytes
		blocks   []lineCoverageBlock
		aligned  bool // the rows start at word boundaries
		// wide is true for uint32 counters, which builds with the race detector
		// store into atomically; they are then read atomically too.
		wide bool
	}

	// lineCoverageFile is a source file of an instrumented package.
	lineCoverageFile struct {
		name    string // path relative to the repository root
		maxLine int32  // last line of the file's blocks and init lines
		// initLines are the executed lines of the file's initialisers, sorted:
		// its init functions and package variable initialisers.
		initLines []int32
		// codeLines is the bitmap of the lines code maps to, in the line table of
		// the package compiled without probes and without inlining, or in that of
		// the binary when the package has none; nil when the table has no lines
		// for the file. Lines without code never count.
		codeLines []byte
	}

	// blockRef identifies a block: the package index in the high 32 bits, the
	// block index in the low ones.
	blockRef uint64

	// LineCoverageWindow collects the lines one test execution runs: the work of
	// its goroutine and of the goroutines started from it, from when the window
	// opens until it closes.
	LineCoverageWindow struct {
		collector *lineCoverageCollector
		slot      uint32     // 0 while paused, or when no slot was free
		blocks    []blockRef // blocks of the slots the window held before
		missed    bool
		closed    bool
		// closing is set, under the collector's mutex, once the window starts
		// giving up its slot: from then on no request handler moves into it.
		closing bool
	}
)

// lineCoverage is the running collector, nil when the test binary was built
// without line coverage.
var lineCoverage atomic.Pointer[lineCoverageCollector]

func noop() {}

// StartLineCoverage starts per-test line coverage when orchestrion built the
// test binary with it, and reports whether it did. Call it once, before the
// tests run.
func StartLineCoverage(sessionID uint64) (started bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("civisibility.linecov: start failed: %v", r)
			started = false
		}
	}()
	if !lineCoverageRuntimeAvailable() || lineCoverage.Load() != nil {
		return false
	}
	c := newLineCoverageCollector(sessionID)
	if c == nil {
		return false
	}
	c.writer = newCoverageWriter()
	lineCoverage.Store(c)
	coveragepropagation.Set(c)
	integrations.PushCiVisibilityCloseAction(c.stop)
	log.Debug("civisibility.linecov: started with %d packages and %d files", len(c.packages), len(c.files))
	return true
}

// IsLineCoverageActive reports whether per-test line coverage is running.
func IsLineCoverageActive() bool {
	return lineCoverage.Load() != nil
}

func newLineCoverageCollector(sessionID uint64) *lineCoverageCollector {
	c := &lineCoverageCollector{sessionID: sessionID}
	fileIndex := map[string]int32{}
	// metaNames maps the file names of the block tables, which are the names
	// the line table of the binary uses, to file indexes.
	metaNames := map[string]int32{}
	indexOf := func(metaName string) int32 {
		if idx, ok := metaNames[metaName]; ok {
			return idx
		}
		name := utils.GetRelativePathFromCITagsSourceRoot(metaName)
		idx, ok := fileIndex[name]
		if !ok {
			idx = int32(len(c.files))
			c.files = append(c.files, lineCoverageFile{name: name})
			fileIndex[name] = idx
		}
		metaNames[metaName] = idx
		return idx
	}
	// closureInitLines are the header lines of the function literals in
	// package-level variable initialisers, by file.
	closureInitLines := map[int32][]int32{}
	// The files whose packages came with a line table, and those whose lines
	// come from the line table of the binary.
	withTable, withoutTable := map[int32]bool{}, map[int32]bool{}
	tableCodeLines, tableVarInitLines := map[int32][]int32{}, map[int32][]int32{}
	for i := 0; ; i++ {
		counters, numBlocks, rowSize, pkgPath, meta, ok := runtimeLineCoveragePackage(i)
		if !ok {
			break
		}
		filesOnly := counters == nil && numBlocks == 0
		if !filesOnly && (counters == nil || numBlocks <= 0 || rowSize%wordSize != 0) {
			log.Debug("civisibility.linecov: skipping package %s: invalid counters", pkgPath)
			continue
		}
		table, err := decodeLineCoverageMeta(meta, numBlocks, indexOf)
		if err != nil {
			log.Debug("civisibility.linecov: skipping package %s: %s", pkgPath, err.Error())
			continue
		}
		for file, lines := range table.initLines {
			closureInitLines[file] = append(closureInitLines[file], lines...)
		}
		if t := table.lines; t != nil {
			for _, file := range table.files {
				withTable[file] = true
			}
			for file, lines := range t.code {
				tableCodeLines[file] = append(tableCodeLines[file], lines...)
			}
			for file, lines := range t.varInit {
				tableVarInitLines[file] = append(tableVarInitLines[file], lines...)
			}
		} else {
			for _, file := range table.files {
				withoutTable[file] = true
			}
		}
		if filesOnly {
			// A package with variable initialisers and no blocks registers its files,
			// so that the lines of its initialisers come from the line table.
			continue
		}
		wide := table.counterSize == 4
		if rowSize < numBlocks*table.counterSize || (wide && uintptr(unsafe.Pointer(counters))%4 != 0) {
			log.Debug("civisibility.linecov: skipping package %s: invalid counters", pkgPath)
			continue
		}
		c.packages = append(c.packages, lineCoveragePackage{
			counters: unsafe.Pointer(counters),
			rowSize:  rowSize,
			blocks:   table.blocks,
			aligned:  uintptr(unsafe.Pointer(counters))%uintptr(wordSize) == 0,
			wide:     wide,
		})
	}
	if len(c.files) == 0 {
		return nil
	}
	for _, p := range c.packages {
		for i := range p.blocks {
			b := &p.blocks[i]
			f := &c.files[b.file]
			f.maxLine = max(f.maxLine, b.endLine)
			for _, line := range b.lines {
				f.maxLine = max(f.maxLine, line)
			}
		}
	}
	for file := range withTable {
		delete(withoutTable, file)
	}
	varInitLines := c.loadLineTable(metaNames, withoutTable)
	for file, lines := range tableCodeLines {
		f := &c.files[file]
		if f.codeLines == nil && !withTable[file] {
			// Only the generic functions that other packages instantiate map code to
			// the file in their line tables: those are not all its code lines, so
			// the file keeps counting every line.
			continue
		}
		for _, line := range lines {
			f.codeLines = setLineGrow(f.codeLines, line)
		}
	}
	for file := range withTable {
		if c.files[file].codeLines == nil {
			// The file has no code: no line of it counts.
			c.files[file].codeLines = []byte{}
		}
	}
	for file, lines := range tableVarInitLines {
		varInitLines[file] = append(varInitLines[file], lines...)
		f := &c.files[file]
		f.maxLine = max(f.maxLine, slices.Max(lines))
	}
	for file, lines := range closureInitLines {
		f := &c.files[file]
		for _, line := range lines {
			// Like the lines blocks credit besides their ranges, only where the line
			// table maps code to them.
			if hasLine(f.codeLines, line) {
				varInitLines[file] = append(varInitLines[file], line)
				f.maxLine = max(f.maxLine, line)
			}
		}
	}

	c.free = make([]uint32, 0, lineCoverageSlotCount-1)
	for slot := uint32(lineCoverageSlotCount - 1); slot > 0; slot-- {
		c.free = append(c.free, slot)
	}

	// Package initialization ran in slot 0, and is over when the tests start.
	bitmaps := map[int32][]byte{}
	for file, lines := range varInitLines {
		bitmap := c.bitmap(bitmaps, file)
		for _, line := range lines {
			setLine(bitmap, line)
		}
	}
	for _, ref := range c.collect(0, false, nil) {
		if b := c.block(ref); b.isInit {
			c.setBlockLines(c.bitmap(bitmaps, b.file), b)
		}
	}
	for file, bitmap := range bitmaps {
		c.files[file].initLines = bitmapLines(bitmap)
	}
	return c
}

// loadLineTable reads from the line table of the binary which lines of the
// wanted files have code. It returns, per file, the lines of the package
// variable initialisers, which ran during package initialization. Files the
// table has no lines for keep a nil code bitmap.
func (c *lineCoverageCollector) loadLineTable(metaNames map[string]int32, wanted map[int32]bool) map[int32][]int32 {
	if runtimeLineCoverageLines == nil || len(wanted) == 0 {
		return map[int32][]int32{}
	}
	start := time.Now()
	varInit := map[int32][]byte{}
	lastName, lastFile := "", int32(-1)
	runtimeLineCoverageLines(
		func(name string) bool {
			idx, ok := metaNames[name]
			return ok && wanted[idx]
		},
		func(funcName, name string, line int32, inlined bool) {
			if lastFile < 0 || name != lastName {
				idx, ok := metaNames[name]
				if !ok || !wanted[idx] {
					return
				}
				lastName, lastFile = name, idx
			}
			f := &c.files[lastFile]
			f.codeLines = setLineGrow(f.codeLines, line)
			if !inlined && isPackageVarInitFunc(funcName) {
				varInit[lastFile] = setLineGrow(varInit[lastFile], line)
			}
		})
	lines := make(map[int32][]int32, len(varInit))
	for file, bitmap := range varInit {
		lines[file] = bitmapLines(bitmap)
		f := &c.files[file]
		f.maxLine = max(f.maxLine, lines[file][len(lines[file])-1])
	}
	found := 0
	for file := range wanted {
		if c.files[file].codeLines != nil {
			found++
		}
	}
	log.Debug("civisibility.linecov: read the line table of the binary for %d of %d files in %s", found, len(wanted), time.Since(start))
	return lines
}

// isPackageVarInitFunc reports whether funcName is a function that the
// compiler generates for the package variable initialisers of a package:
// <pkg>.init, or <pkg>.map.init.<n> for large map literals. Dots in the last
// element of the package path are escaped in function names, and the user's
// init functions are <pkg>.init.<n>.
func isPackageVarInitFunc(funcName string) bool {
	pkg, ok := strings.CutSuffix(funcName, ".init")
	if !ok {
		i := strings.LastIndexByte(funcName, '.')
		if i < 0 || !isDecimal(funcName[i+1:]) {
			return false
		}
		if pkg, ok = strings.CutSuffix(funcName[:i], ".map.init"); !ok {
			return false
		}
	}
	return pkg != "" && !strings.Contains(pkg[strings.LastIndexByte(pkg, '/')+1:], ".")
}

func isDecimal(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// OpenLineCoverageWindow opens the line coverage window of a test execution on
// the calling goroutine. It returns nil when line coverage is off.
func OpenLineCoverageWindow() (w *LineCoverageWindow) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("civisibility.linecov: opening a window failed: %v", r)
			w = nil
		}
	}()
	c := lineCoverage.Load()
	if c == nil {
		return nil
	}
	w = &LineCoverageWindow{collector: c}
	w.attach()
	return w
}

// attach gives the window a slot, and sets it on the calling goroutine.
// Without a free slot the goroutine gets slot 0, so that its work goes to no
// test rather than to a parent test.
func (w *LineCoverageWindow) attach() {
	w.slot = w.collector.lease(w)
	if w.slot == 0 {
		w.missed = true
	}
	runtimeLineCoverageSetSlot(w.slot)
}

// PauseLineCoverageWindow releases the slot of the calling goroutine's window
// while its test waits in t.Parallel, so that waiting tests do not use up the
// slots. It returns the function that takes a slot again when the wait ends.
func PauseLineCoverageWindow() (resume func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("civisibility.linecov: pausing a window failed: %v", r)
			resume = noop
		}
	}()
	c := lineCoverage.Load()
	if c == nil {
		return noop
	}
	slot := runtimeLineCoverageSlot()
	if slot == 0 || slot >= lineCoverageSlotCount {
		return noop
	}
	c.mu.Lock()
	w := c.windows[slot]
	c.mu.Unlock()
	if w == nil || !w.pause() {
		return noop
	}
	return w.resume
}

// pause releases the window's slot. It keeps the slot, and returns false, when
// other goroutines carry it.
func (w *LineCoverageWindow) pause() bool {
	slot := w.slot
	if w.closed || slot == 0 || runtimeLineCoverageSlot() != slot {
		return false
	}
	// Under the mutex, so that no request handler moves into the slot between
	// the check and the release.
	w.collector.mu.Lock()
	if runtimeLineCoverageSlotRefs(slot) != 1 {
		w.collector.mu.Unlock()
		return false
	}
	w.closing = true
	w.collector.mu.Unlock()
	runtimeLineCoverageSetSlot(0)
	w.slot = 0
	w.blocks = w.collector.collect(slot, true, w.blocks)
	w.collector.release(slot, false, true)
	return true
}

func (w *LineCoverageWindow) resume() {
	defer func() {
		if r := recover(); r != nil {
			log.Error("civisibility.linecov: resuming a window failed: %v", r)
		}
	}()
	if !w.closed && w.slot == 0 {
		w.attach()
	}
}

// Close closes the window, and sends the coverage of the test execution with
// the given suite and test IDs. Call it after the test's cleanups ran.
func (w *LineCoverageWindow) Close(suiteID, testID uint64) {
	if w == nil || w.closed {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error("civisibility.linecov: closing a window failed: %v", r)
		}
	}()
	w.closed = true
	c := w.collector
	if slot := w.slot; slot != 0 {
		// Before the detach, so that it also moves a handler that moved in.
		c.mu.Lock()
		w.closing = true
		c.mu.Unlock()
		w.slot = 0
		if runtimeLineCoverageSlot() == slot {
			runtimeLineCoverageSetSlot(0)
		}
		dirty, detached := detachSlot(slot)
		w.blocks = c.collect(slot, true, w.blocks)
		c.release(slot, dirty, detached)
	}
	if w.missed {
		// A partial record would claim the test does not need lines it ran.
		return
	}
	c.send(&ciTestCoverageData{
		SessionID: c.sessionID,
		SuiteID:   suiteID,
		SpanID:    testID,
		Files:     c.fileCoverage(w.blocks, true),
	})
}

// detachSlot moves the goroutines that still carry slot to slot 0, so that
// their later work goes to no test. dirty reports whether one of them may
// still store into the slot's rows; detached is false when goroutines kept
// taking the slot.
func detachSlot(slot uint32) (dirty, detached bool) {
	for attempt := 0; attempt < maxDetachAttempts; attempt++ {
		if runtimeLineCoverageSlotRefs(slot) <= 0 {
			return dirty, true
		}
		if attempt > 0 {
			runtime.Gosched()
		}
		if _, running := runtimeLineCoverageRetarget(slot); running {
			dirty = true
		}
	}
	return dirty, runtimeLineCoverageSlotRefs(slot) <= 0
}

// lease takes a slot for w, or returns 0 when none is free.
func (c *lineCoverageCollector) lease(w *LineCoverageWindow) uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var slot uint32
	switch {
	case c.stopped:
		return 0
	case len(c.free) > 0:
		slot = c.free[len(c.free)-1]
		c.free = c.free[:len(c.free)-1]
	case len(c.dirty) > 0:
		slot = c.dirty[0]
		c.dirty = c.dirty[1:]
		// What the slot got after its window closed belongs to no test.
		c.late = c.collect(slot, true, c.late)
	default:
		c.missed++
		return 0
	}
	c.windows[slot] = w
	w.closing = false
	return slot
}

func (c *lineCoverageCollector) release(slot uint32, dirty, detached bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.windows[slot] = nil
	switch {
	case !detached:
		log.Debug("civisibility.linecov: slot %d is still carried by goroutines; it will not be reused", slot)
		c.poisoned = append(c.poisoned, slot)
	case dirty:
		c.dirty = append(c.dirty, slot)
	default:
		c.free = append(c.free, slot)
	}
}

// collect appends to dst the blocks stored in the rows of slot. When reset is
// true, it also clears the rows, so that each store is collected once.
func (c *lineCoverageCollector) collect(slot uint32, reset bool, dst []blockRef) []blockRef {
	for pi := range c.packages {
		p := &c.packages[pi]
		row := unsafe.Add(p.counters, int(slot)*p.rowSize)
		n := len(p.blocks)
		if p.wide {
			for i := range n {
				counter := (*uint32)(unsafe.Add(row, 4*i))
				if atomic.LoadUint32(counter) == 0 {
					continue
				}
				if reset {
					atomic.StoreUint32(counter, 0)
				}
				dst = append(dst, newBlockRef(pi, i))
			}
			continue
		}
		if !p.aligned {
			for i := range n {
				b := (*byte)(unsafe.Add(row, i))
				if *b == 0 {
					continue
				}
				if reset {
					*b = 0
				}
				dst = append(dst, newBlockRef(pi, i))
			}
			continue
		}
		for off := 0; off < n; off += wordSize {
			word := (*uintptr)(unsafe.Add(row, off))
			v := atomic.LoadUintptr(word)
			if v == 0 {
				continue
			}
			if reset {
				v = atomic.SwapUintptr(word, 0)
			}
			for i, b := range (*[wordSize]byte)(unsafe.Pointer(&v)) {
				if b != 0 && off+i < n {
					dst = append(dst, newBlockRef(pi, off+i))
				}
			}
		}
	}
	return dst
}

func newBlockRef(pkg, block int) blockRef {
	return blockRef(uint64(pkg)<<32 | uint64(uint32(block)))
}

func (c *lineCoverageCollector) block(ref blockRef) *lineCoverageBlock {
	return &c.packages[ref>>32].blocks[uint32(ref)]
}

// bitmap returns the line bitmap of file in bitmaps, adding it when missing.
func (c *lineCoverageCollector) bitmap(bitmaps map[int32][]byte, file int32) []byte {
	bitmap, ok := bitmaps[file]
	if !ok {
		bitmap = make([]byte, (int(c.files[file].maxLine)+7)/8)
		bitmaps[file] = bitmap
	}
	return bitmap
}

// fileCoverage returns the lines of the blocks per file. Init blocks are left
// out: their lines go to the init part of Shared, and to each test that runs
// code of their file, which withInit adds.
func (c *lineCoverageCollector) fileCoverage(refs []blockRef, withInit bool) []*ciTestCoverageFile {
	bitmaps := map[int32][]byte{}
	for _, ref := range refs {
		if b := c.block(ref); !b.isInit {
			c.setBlockLines(c.bitmap(bitmaps, b.file), b)
		}
	}
	if withInit {
		for file, bitmap := range bitmaps {
			for _, line := range c.files[file].initLines {
				setLine(bitmap, line)
			}
		}
	}
	return c.coverageFiles(bitmaps)
}

// coverageFiles returns the files of bitmaps sorted by name, each with the
// bitmap up to its last line set.
func (c *lineCoverageCollector) coverageFiles(bitmaps map[int32][]byte) []*ciTestCoverageFile {
	files := make([]*ciTestCoverageFile, 0, len(bitmaps))
	for file, bitmap := range bitmaps {
		end := len(bitmap)
		for end > 0 && bitmap[end-1] == 0 {
			end--
		}
		if end > 0 {
			files = append(files, &ciTestCoverageFile{FileName: c.files[file].name, Bitmap: bitmap[:end]})
		}
	}
	slices.SortFunc(files, func(a, b *ciTestCoverageFile) int { return cmp.Compare(a.FileName, b.FileName) })
	return files
}

// setBlockLines sets in bitmap the lines of b that have code: the lines of its
// range, and the lines it credits besides. Without a line table for the file,
// it sets every line of the range, and none of the others.
func (c *lineCoverageCollector) setBlockLines(bitmap []byte, b *lineCoverageBlock) {
	code := c.files[b.file].codeLines
	for line := b.startLine; line <= b.endLine; line++ {
		if code == nil || hasLine(code, line) {
			setLine(bitmap, line)
		}
	}
	if code == nil {
		return
	}
	for _, line := range b.lines {
		if hasLine(code, line) {
			setLine(bitmap, line)
		}
	}
}

// setLine sets line in bitmap, in FileBitmap order: line L is bit L-1, most
// significant bit first. Lines past the end of bitmap are left out.
func setLine(bitmap []byte, line int32) {
	if idx := int(line) - 1; idx >= 0 && idx/8 < len(bitmap) {
		bitmap[idx/8] |= 0x80 >> (idx % 8)
	}
}

// setLineGrow sets line in bitmap, growing bitmap to hold it.
func setLineGrow(bitmap []byte, line int32) []byte {
	idx := int(line) - 1
	if idx < 0 {
		return bitmap
	}
	if n := idx/8 + 1; n > len(bitmap) {
		bitmap = append(bitmap, make([]byte, n-len(bitmap))...)
	}
	bitmap[idx/8] |= 0x80 >> (idx % 8)
	return bitmap
}

func hasLine(bitmap []byte, line int32) bool {
	idx := int(line) - 1
	return idx >= 0 && idx/8 < len(bitmap) && bitmap[idx/8]&(0x80>>(idx%8)) != 0
}

// bitmapLines returns the lines set in bitmap, in increasing order.
func bitmapLines(bitmap []byte) []int32 {
	var lines []int32
	for i, b := range bitmap {
		for bit := 0; b != 0; bit++ {
			if b&0x80 != 0 {
				lines = append(lines, int32(i*8+bit+1))
			}
			b <<= 1
		}
	}
	return lines
}

func (c *lineCoverageCollector) send(record coverageRecord) {
	c.mu.Lock()
	stopped := c.stopped
	c.mu.Unlock()
	if !stopped {
		c.writer.addRecord(record)
	}
}

// stop sends the Shared records of the process, and stops the writer.
func (c *lineCoverageCollector) stop() {
	defer func() {
		if r := recover(); r != nil {
			log.Error("civisibility.linecov: stopping failed: %v", r)
		}
	}()
	if !lineCoverage.CompareAndSwap(c, nil) {
		return
	}
	coveragepropagation.Set(nil)

	c.mu.Lock()
	unowned := c.collect(0, false, nil)
	unowned = append(unowned, c.late...)
	for _, slot := range c.dirty {
		unowned = c.collect(slot, false, unowned)
	}
	for _, slot := range c.poisoned {
		unowned = c.collect(slot, false, unowned)
	}
	missed := c.missed
	c.mu.Unlock()
	if missed > 0 {
		log.Warn("civisibility.linecov: %d test executions ran without a coverage slot, and have no coverage record", missed)
	}

	initBitmaps := map[int32][]byte{}
	for file := range c.files {
		for _, line := range c.files[file].initLines {
			setLine(c.bitmap(initBitmaps, int32(file)), line)
		}
	}
	for _, part := range []*ciTestCoverageSharedData{
		{SessionID: c.sessionID, Part: sharedPartInit, Files: c.coverageFiles(initBitmaps)},
		{SessionID: c.sessionID, Part: sharedPartUnowned, Files: c.fileCoverage(unowned, false)},
	} {
		if len(part.Files) > 0 {
			c.writer.addRecord(part)
		}
	}

	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()
	c.writer.stop()
}
