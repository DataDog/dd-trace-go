// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

type retryAttemptGroup struct {
	mu                    locking.Mutex
	executionMu           locking.Mutex
	original              *testing.T
	originalParentBarrier <-chan bool
	rootParallelObserved  bool
	originalTransitioned  bool
	originalParallelReady chan struct{}
	rootParallelBridge    func() error
	matcher               *retryAttemptMatcherTransaction
	attempts              []*retryAttemptRoot
	retired               bool
}

type retryAttemptMatcherTransaction struct {
	matcher  *contextMatcher
	prefix   string
	baseline map[string]int32
}

// retryAttemptRoot contains the fresh testing state for one local attempt.
// The synthetic parent prevents attempt-local failure and skip state from
// propagating into the original testing.T before aggregate finalization.
type retryAttemptRoot struct {
	original              *testing.T
	group                 *retryAttemptGroup
	test                  *testing.T
	parent                *testing.T
	layout                *testingInternalsLayout
	raceBaseline          int64
	parallelLeaseTransfer atomic.Bool
	parallelPauseDuration time.Duration
	cleanupObservation    atomic.Uint32
	generationFrozen      atomic.Bool
	generationFailed      atomic.Bool
	terminalMu            locking.Mutex
	terminalTrace         []retryAttemptTerminal
	outputCapture         retryAttemptOutputCapture
	metadata              *testExecutionMetadata
}

type retryAttemptOutputCapture struct {
	mu     locking.Mutex
	output []byte
}

type retryAttemptCaptureWriter struct {
	capture *retryAttemptOutputCapture
	writer  io.Writer
}

func (w retryAttemptCaptureWriter) Write(p []byte) (int, error) {
	w.capture.mu.Lock()
	w.capture.output = append(w.capture.output, p...)
	w.capture.mu.Unlock()
	return w.writer.Write(p)
}

func (c *retryAttemptOutputCapture) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.output...)
}

func newRetryAttemptRoot(original *testing.T) (*retryAttemptRoot, string) {
	group, reason := newRetryAttemptGroup(original)
	if reason != "" {
		return nil, reason
	}
	return newRetryAttemptRootInGroup(group)
}

func newRetryAttemptGroup(original *testing.T) (*retryAttemptGroup, string) {
	layout, reason := getRetryAttemptLayout()
	if reason != "" {
		return nil, reason
	}
	if original == nil {
		return nil, "missing_original_test"
	}
	originalBase := commonBaseForTest(original, layout)
	if originalBase == nil {
		return nil, "testing_t_layout_unsupported"
	}
	originalParentBase := pointerWord(originalBase, layout.common.parent)
	if originalParentBase == nil {
		return nil, "missing_original_parent"
	}
	originalMu := fieldPtr[sync.RWMutex](originalBase, layout.common.mu)
	originalMu.RLock()
	inFuzzFn := *fieldPtr[bool](originalBase, layout.common.inFuzzFn)
	isSynctest := layout.common.isSynctest.available && *fieldPtr[bool](originalBase, layout.common.isSynctest)
	originalMu.RUnlock()
	if inFuzzFn {
		return nil, "fuzz_active"
	}
	if isSynctest {
		return nil, "synctest_unsupported"
	}
	group := &retryAttemptGroup{
		original:              original,
		originalParentBarrier: *fieldPtr[chan bool](originalParentBase, layout.common.barrier),
	}
	name := *fieldPtr[string](originalBase, layout.common.name)
	matcher := getTestContextMatcherPrivateFields(original)
	if matcher == nil || matcher.mu == nil || matcher.subNames == nil {
		return nil, "testing_matcher_unsupported"
	}
	group.matcher = newRetryAttemptMatcherTransaction(matcher, name+"/")
	return group, ""
}

func newRetryAttemptMatcherTransaction(matcher *contextMatcher, prefix string) *retryAttemptMatcherTransaction {
	tx := &retryAttemptMatcherTransaction{matcher: matcher, prefix: prefix, baseline: make(map[string]int32)}
	matcher.mu.Lock()
	defer matcher.mu.Unlock()
	if matcher.subNames == nil || *matcher.subNames == nil {
		return tx
	}
	for name, count := range *matcher.subNames {
		if strings.HasPrefix(name, prefix) {
			tx.baseline[name] = count
		}
	}
	return tx
}

func (tx *retryAttemptMatcherTransaction) restore() {
	if tx == nil || tx.matcher == nil || tx.matcher.mu == nil || tx.matcher.subNames == nil {
		return
	}
	tx.matcher.mu.Lock()
	defer tx.matcher.mu.Unlock()
	if *tx.matcher.subNames == nil {
		*tx.matcher.subNames = make(map[string]int32, len(tx.baseline))
	}
	for name := range *tx.matcher.subNames {
		if strings.HasPrefix(name, tx.prefix) {
			delete(*tx.matcher.subNames, name)
		}
	}
	for name, count := range tx.baseline {
		(*tx.matcher.subNames)[name] = count
	}
}

func newRetryAttemptRootInGroup(group *retryAttemptGroup) (*retryAttemptRoot, string) {
	layout, reason := getRetryAttemptLayout()
	if reason != "" {
		return nil, reason
	}
	if group == nil || group.original == nil {
		return nil, "missing_retry_attempt_group"
	}
	original := group.original

	raceBaseline := retryAttemptRaceErrors()
	originalBase := commonBaseForTest(original, layout)
	if originalBase == nil {
		return nil, "testing_t_layout_unsupported"
	}
	originalParentBase := pointerWord(originalBase, layout.common.parent)
	if originalParentBase == nil {
		return nil, "missing_original_parent"
	}
	originalMu := fieldPtr[sync.RWMutex](originalBase, layout.common.mu)
	originalMu.RLock()
	inFuzzFn := *fieldPtr[bool](originalBase, layout.common.inFuzzFn)
	isSynctest := layout.common.isSynctest.available && *fieldPtr[bool](originalBase, layout.common.isSynctest)
	originalMu.RUnlock()
	if inFuzzFn {
		return nil, "fuzz_active"
	}
	if isSynctest {
		return nil, "synctest_unsupported"
	}

	root := createNewTestFast(layout)
	parent := createNewTestFast(layout)
	rootBase := commonBaseForTest(root, layout)
	parentBase := commonBaseForTest(parent, layout)
	if rootBase == nil || parentBase == nil {
		return nil, "testing_t_layout_unsupported"
	}

	if !copyRetryAttemptStableCommon(originalBase, rootBase, layout) {
		return nil, "testing_t_layout_unsupported"
	}
	if !copyRetryAttemptStableCommon(originalParentBase, parentBase, layout) {
		return nil, "testing_t_parent_layout_unsupported"
	}

	setPrivatePointerField(
		layout.common.parent.typ,
		fieldRawPtr(rootBase, layout.common.parent.unsafeField),
		unsafe.Pointer(parentBase),
	)
	copyWordField(unsafe.Pointer(original), unsafe.Pointer(root), layout.tstate)
	initializeRetryAttemptFreshState(rootBase, layout, raceBaseline)
	initializeRetryAttemptFreshState(parentBase, layout, raceBaseline)
	attempt := &retryAttemptRoot{original: original, group: group, test: root, parent: parent, layout: layout, raceBaseline: raceBaseline}
	group.mu.Lock()
	if group.retired {
		group.mu.Unlock()
		attempt.cancelContexts()
		return nil, "retry_attempt_group_retired"
	}
	group.attempts = append(group.attempts, attempt)
	group.mu.Unlock()
	attachRetryAttemptChattyCapture(rootBase, layout, &attempt.outputCapture)
	attachRetryAttemptChattyCapture(parentBase, layout, &attempt.outputCapture)
	initializeRetryAttemptWriter(originalBase, rootBase, layout, true)
	initializeRetryAttemptWriter(originalParentBase, parentBase, layout, false)
	copyTypedField[bool](unsafe.Pointer(original), unsafe.Pointer(root), layout.denyParallel)
	copyTypedField[bool](originalParentBase, unsafe.Pointer(parent), layout.denyParallel)
	group.mu.Lock()
	rootParallelObserved := group.rootParallelObserved
	group.mu.Unlock()
	if rootParallelObserved {
		*fieldPtr[bool](parentBase, layout.common.isParallel) = true
	}
	initializeRetryAttemptStart(rootBase, layout.common.start.unsafeField)
	initializeRetryAttemptStart(parentBase, layout.common.start.unsafeField)
	reinitOutputWriterFast(root, layout)
	reinitOutputWriterFast(parent, layout)

	runtime.KeepAlive(original)
	runtime.KeepAlive(root)
	runtime.KeepAlive(parent)
	return attempt, ""
}

func (g *retryAttemptGroup) retire() {
	if g == nil {
		return
	}
	g.executionMu.Lock()
	defer g.executionMu.Unlock()
	g.matcher.restore()

	g.mu.Lock()
	if g.retired {
		g.mu.Unlock()
		return
	}
	g.retired = true
	attempts := append([]*retryAttemptRoot(nil), g.attempts...)
	g.mu.Unlock()

	for _, attempt := range attempts {
		if attempt.metadata != nil {
			deleteTestMetadata(attempt.test)
			attempt.metadata = nil
		}
		for _, current := range []*testing.T{attempt.test, attempt.parent} {
			base := commonBaseForTest(current, attempt.layout)
			mu := fieldPtr[sync.RWMutex](base, attempt.layout.common.mu)
			mu.Lock()
			*fieldPtr[bool](base, attempt.layout.common.done) = true
			mu.Unlock()
		}
		attempt.cancelContexts()
	}
}

func (g *retryAttemptGroup) hasLateFailure() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	attempts := append([]*retryAttemptRoot(nil), g.attempts...)
	g.mu.Unlock()
	for _, attempt := range attempts {
		if attempt == nil || !attempt.generationFrozen.Load() || attempt.generationFailed.Load() {
			continue
		}
		base := commonBaseForTest(attempt.parent, attempt.layout)
		mu := fieldPtr[sync.RWMutex](base, attempt.layout.common.mu)
		mu.RLock()
		failed := *fieldPtr[bool](base, attempt.layout.common.failed)
		mu.RUnlock()
		if failed {
			return true
		}
	}
	return false
}

func (g *retryAttemptGroup) observeProcessRootParallel() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.rootParallelObserved = true
	g.mu.Unlock()
}

// transitionOriginalToParallel transfers the logical test's package scheduler
// lease exactly once. Access to the private scheduler state remains reflection-
// based because supported Go toolchains reject new linkname access to testing.
func (g *retryAttemptGroup) transitionOriginalToParallel() bool {
	if g == nil || g.original == nil {
		return false
	}
	layout, _ := getRetryAttemptLayout()
	if layout == nil {
		return false
	}
	originalBase := commonBaseForTest(g.original, layout)
	originalParentBase := pointerWord(originalBase, layout.common.parent)
	if originalBase == nil || originalParentBase == nil {
		return false
	}

	g.mu.Lock()
	g.rootParallelObserved = true
	if g.originalTransitioned {
		ready := g.originalParallelReady
		g.mu.Unlock()
		if ready != nil {
			<-ready
		}
		return true
	}
	g.originalTransitioned = true
	g.originalParallelReady = make(chan struct{})
	ready := g.originalParallelReady
	addRetryAttemptElapsed(originalBase, layout)
	*fieldPtr[bool](originalBase, layout.common.isParallel) = true
	*fieldPtr[[]*testing.T](originalParentBase, layout.common.sub) = append(
		*fieldPtr[[]*testing.T](originalParentBase, layout.common.sub),
		g.original,
	)
	*fieldPtr[chan bool](originalBase, layout.common.signal) <- true
	g.mu.Unlock()

	<-g.originalParentBarrier
	g.mu.Lock()
	close(ready)
	g.mu.Unlock()
	return false
}

func setRetryAttemptLogicalDeadline(attempt *retryAttemptRoot, deadline time.Time, present bool) bool {
	if attempt == nil || attempt.test == nil || attempt.layout == nil || !attempt.layout.testState.deadline.available {
		return false
	}
	state := getTestState(attempt.test)
	if state == nil {
		return false
	}
	if !present {
		deadline = time.Time{}
	}
	*fieldPtr[time.Time](unsafe.Pointer(state), attempt.layout.testState.deadline) = deadline
	return true
}

func (r *retryAttemptRoot) freezeGenerationFailure() {
	if r == nil || r.generationFrozen.Load() {
		return
	}
	base := commonBaseForTest(r.parent, r.layout)
	mu := fieldPtr[sync.RWMutex](base, r.layout.common.mu)
	mu.RLock()
	failed := *fieldPtr[bool](base, r.layout.common.failed)
	mu.RUnlock()
	r.generationFailed.Store(failed)
	r.generationFrozen.Store(true)
}

func copyRetryAttemptStableCommon(sourceBase, targetBase unsafe.Pointer, layout *testingInternalsLayout) bool {
	if sourceBase == nil || targetBase == nil || layout == nil || !layout.retryAttemptOK {
		return false
	}
	sourceMu := fieldPtr[sync.RWMutex](sourceBase, layout.common.mu)
	if sourceMu == nil {
		return false
	}
	sourceMu.RLock()
	defer sourceMu.RUnlock()

	copyTypedField[bool](sourceBase, targetBase, layout.common.inFuzzFn)
	copyRetryAttemptHelperPCs(sourceBase, targetBase, layout)
	cloneRetryAttemptChatty(sourceBase, targetBase, layout)
	copyTypedField[string](sourceBase, targetBase, layout.common.runner)
	copyTypedField[int](sourceBase, targetBase, layout.common.level)
	copyConvertedField[[]uintptr](sourceBase, targetBase, layout.common.creator, slices.Clone[[]uintptr])
	copyTypedField[string](sourceBase, targetBase, layout.common.name)
	if layout.common.modulePath.available {
		copyTypedField[string](sourceBase, targetBase, layout.common.modulePath)
	}
	if layout.common.importPath.available {
		copyTypedField[string](sourceBase, targetBase, layout.common.importPath)
	}
	return true
}

const retryAttemptOutputMarker = byte(0x16)

type retryAttemptIndenter struct {
	base   unsafe.Pointer
	layout *testingInternalsLayout
}

func (w retryAttemptIndenter) Write(p []byte) (int, error) {
	written := len(p)
	output := fieldPtr[[]byte](w.base, w.layout.common.output)
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		if end == -1 {
			end = len(p)
		} else {
			end++
		}
		line := p[:end]
		if len(line) > 0 && line[0] == retryAttemptOutputMarker {
			*output = append(*output, retryAttemptOutputMarker)
			line = line[1:]
		}
		*output = append(*output, "    "...)
		*output = append(*output, line...)
		p = p[end:]
	}
	return written, nil
}

func initializeRetryAttemptWriter(sourceBase, targetBase unsafe.Pointer, layout *testingInternalsLayout, preserveChattyDestination bool) {
	targetWriter := io.Writer(retryAttemptIndenter{base: targetBase, layout: layout})
	sourceChatty := pointerWord(sourceBase, layout.common.chatty)
	targetChatty := pointerWord(targetBase, layout.common.chatty)
	if preserveChattyDestination && sourceChatty != nil && targetChatty != nil {
		sourceWriter := *fieldPtr[io.Writer](sourceBase, layout.common.w)
		sourceChattyWriter := *fieldPtr[io.Writer](sourceChatty, layout.chattyPrinter.w)
		if sourceWriter == sourceChattyWriter {
			targetWriter = *fieldPtr[io.Writer](targetChatty, layout.chattyPrinter.w)
		}
	}
	*fieldPtr[io.Writer](targetBase, layout.common.w) = targetWriter
}

func attachRetryAttemptChattyCapture(base unsafe.Pointer, layout *testingInternalsLayout, capture *retryAttemptOutputCapture) {
	chatty := pointerWord(base, layout.common.chatty)
	if chatty == nil {
		return
	}
	writer := *fieldPtr[io.Writer](chatty, layout.chattyPrinter.w)
	*fieldPtr[io.Writer](chatty, layout.chattyPrinter.w) = retryAttemptCaptureWriter{capture: capture, writer: writer}
}

func copyRetryAttemptHelperPCs(sourceBase, targetBase unsafe.Pointer, layout *testingInternalsLayout) {
	source := *fieldPtr[map[uintptr]struct{}](sourceBase, layout.common.helperPCs)
	target := make(map[uintptr]struct{}, len(source))
	for pc := range source {
		target[pc] = struct{}{}
	}
	*fieldPtr[map[uintptr]struct{}](targetBase, layout.common.helperPCs) = target
}

func cloneRetryAttemptChatty(sourceBase, targetBase unsafe.Pointer, layout *testingInternalsLayout) {
	source := pointerWord(sourceBase, layout.common.chatty)
	if source == nil {
		setPrivatePointerField(layout.common.chatty.typ, fieldRawPtr(targetBase, layout.common.chatty.unsafeField), nil)
		return
	}

	value := reflect.New(layout.common.chatty.typ.Elem())
	target := unsafe.Pointer(value.Pointer())
	copyConvertedField[io.Writer](source, target, layout.chattyPrinter.w, getThreadSafeWriter)
	copyTypedField[bool](source, target, layout.chattyPrinter.json)
	setPrivatePointerField(layout.common.chatty.typ, fieldRawPtr(targetBase, layout.common.chatty.unsafeField), target)
	runtime.KeepAlive(value)
}

func initializeRetryAttemptFreshState(base unsafe.Pointer, layout *testingInternalsLayout, raceBaseline int64) {
	*fieldPtr[[]byte](base, layout.common.output) = nil
	*fieldPtr[bool](base, layout.common.ran) = false
	*fieldPtr[bool](base, layout.common.failed) = false
	*fieldPtr[bool](base, layout.common.skipped) = false
	*fieldPtr[bool](base, layout.common.done) = false
	*fieldPtr[map[string]struct{}](base, layout.common.helperNames) = nil
	*fieldPtr[[]func()](base, layout.common.cleanups) = nil
	*fieldPtr[string](base, layout.common.cleanupName) = ""
	*fieldPtr[[]uintptr](base, layout.common.cleanupPc) = nil
	*fieldPtr[bool](base, layout.common.finished) = false
	if layout.common.isSynctest.available {
		*fieldPtr[bool](base, layout.common.isSynctest) = false
	}
	*fieldPtr[atomic.Bool](base, layout.common.hasSub) = atomic.Bool{}
	*fieldPtr[atomic.Bool](base, layout.common.cleanupStarted) = atomic.Bool{}
	*fieldPtr[bool](base, layout.common.isParallel) = false
	*fieldPtr[time.Duration](base, layout.common.duration) = 0
	*fieldPtr[[]*testing.T](base, layout.common.sub) = nil
	*fieldPtr[atomic.Int64](base, layout.common.lastRaceErrors) = atomic.Int64{}
	fieldPtr[atomic.Int64](base, layout.common.lastRaceErrors).Store(raceBaseline)
	*fieldPtr[atomic.Bool](base, layout.common.raceErrorLogged) = atomic.Bool{}
	*fieldPtr[string](base, layout.common.tempDir) = ""
	*fieldPtr[error](base, layout.common.tempDirErr) = nil
	*fieldPtr[int32](base, layout.common.tempDirSeq) = 0
	if layout.common.isEnvSet.available {
		*fieldPtr[bool](base, layout.common.isEnvSet) = false
	}
	if layout.common.ctx.available {
		if layout.common.cancelCtx.available {
			if cancel := *fieldPtr[context.CancelFunc](base, layout.common.cancelCtx); cancel != nil {
				cancel()
			}
			ctx, cancel := context.WithCancel(context.Background())
			*fieldPtr[context.Context](base, layout.common.ctx) = ctx
			*fieldPtr[context.CancelFunc](base, layout.common.cancelCtx) = cancel
		} else {
			*fieldPtr[context.Context](base, layout.common.ctx) = context.Background()
		}
	}
}

func (r *retryAttemptRoot) cancelContexts() {
	if r == nil {
		return
	}
	for _, current := range []*testing.T{r.test, r.parent} {
		layout := r.layout
		if current == nil || layout == nil || !layout.common.cancelCtx.available {
			continue
		}
		base := commonBaseForTest(current, layout)
		if base == nil {
			continue
		}
		if cancel := *fieldPtr[context.CancelFunc](base, layout.common.cancelCtx); cancel != nil {
			cancel()
		}
	}
}
