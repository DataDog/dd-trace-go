// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"context"
	"io"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// unsafeField describes a private field discovered from a runtime type.
// Offset zero is a valid offset, so callers must use available instead of
// treating a zero offset as "not found".
type unsafeField struct {
	name      string
	offset    uintptr
	size      uintptr
	typ       reflect.Type
	available bool
	optional  bool
}

// wordCopiedField marks a field that the existing implementation copies as a
// single pointer-sized word. This preserves compatibility for fields that were
// intentionally accessed through unsafe.Pointer instead of their real type.
type wordCopiedField struct {
	unsafeField
}

// commonFieldsLayout stores the private testing.common fields used by the
// gotesting integration. These offsets are always relative to a testing.common
// base pointer, not directly to a testing.T or testing.B pointer.
type commonFieldsLayout struct {
	mu              unsafeField
	output          unsafeField
	w               unsafeField
	ran             unsafeField
	failed          unsafeField
	skipped         unsafeField
	done            unsafeField
	helperPCs       unsafeField
	helperNames     unsafeField
	cleanups        unsafeField
	cleanupName     unsafeField
	cleanupPc       unsafeField
	finished        unsafeField
	inFuzzFn        unsafeField
	chatty          wordCopiedField
	bench           unsafeField
	hasSub          unsafeField
	cleanupStarted  unsafeField
	runner          unsafeField
	isParallel      unsafeField
	parent          wordCopiedField
	level           unsafeField
	creator         unsafeField
	name            unsafeField
	start           wordCopiedField
	duration        unsafeField
	barrier         unsafeField
	signal          unsafeField
	sub             unsafeField
	lastRaceErrors  unsafeField
	raceErrorLogged unsafeField
	tempDir         unsafeField
	tempDirErr      unsafeField
	tempDirSeq      unsafeField
	isEnvSet        unsafeField
	context         wordCopiedField
	ctx             unsafeField
	cancelCtx       unsafeField
	o               unsafeField
}

// contextMatcherLayout stores the private matcher fields reached through
// testing.T's context or tstate pointer. The source pointer field is optional
// because Go renamed this path across releases.
type contextMatcherLayout struct {
	sourceField wordCopiedField
	match       wordCopiedField
	mu          unsafeField
	subNames    unsafeField
}

// chattyPrinterLayout stores the fields needed to wrap verbose test output.
// The layout is only valid when the runtime exposes the expected private
// chattyPrinter shape.
type chattyPrinterLayout struct {
	w        unsafeField
	lastName unsafeField
}

// outputWriterLayout stores the Go 1.25+ output writer internals. These fields
// are optional because older Go layouts do not expose testing.common.o.
type outputWriterLayout struct {
	typ     reflect.Type
	c       unsafeField
	partial unsafeField
}

// benchmarkFieldsLayout stores private testing.B fields that are outside the
// embedded testing.common value.
type benchmarkFieldsLayout struct {
	benchFunc unsafeField
	result    unsafeField
}

// testingInternalsLayout is the process-local description of the runtime
// testing package layout. Section flags are computed once so hot helpers do not
// repeat compatibility decisions on every test execution.
type testingInternalsLayout struct {
	disabled bool

	tCommon unsafeField
	bCommon unsafeField
	common  commonFieldsLayout

	tstate         wordCopiedField
	denyParallel   unsafeField
	contextMatcher contextMatcherLayout
	chattyPrinter  chattyPrinterLayout
	outputWriter   outputWriterLayout
	benchmark      benchmarkFieldsLayout

	testFieldsOK      bool
	parentFieldsOK    bool
	contextMatcherOK  bool
	copyTestOK        bool
	createTestOK      bool
	outputWriterOK    bool
	chattyOK          bool
	benchmarkFieldsOK bool
}

var (
	testingInternalsLayoutOnce  sync.Once
	testingInternalsLayoutValue *testingInternalsLayout
)

// getTestingInternalsLayout returns the cached runtime testing layout. If layout
// discovery sees an unexpected shape, the returned layout has fast paths disabled
// and callers fall back to the reflection implementation.
func getTestingInternalsLayout() *testingInternalsLayout {
	testingInternalsLayoutOnce.Do(func() {
		testingInternalsLayoutValue = buildTestingInternalsLayout(
			reflect.TypeFor[testing.T](),
			reflect.TypeFor[testing.B](),
		)
	})
	return testingInternalsLayoutValue
}

// buildTestingInternalsLayout builds a layout from explicit types. Keeping this
// pure lets tests exercise invalid layouts without resetting the package-level
// sync.Once used by production code.
func buildTestingInternalsLayout(tType, bType reflect.Type) (layout *testingInternalsLayout) {
	defer func() {
		if recover() != nil {
			layout = &testingInternalsLayout{disabled: true}
		}
	}()

	l := &testingInternalsLayout{}
	if tType.Kind() != reflect.Struct || bType.Kind() != reflect.Struct {
		l.disabled = true
		return l
	}

	commonFromT, ok := exactField(tType, "common", nil, false)
	if !ok || commonFromT.offset != 0 || commonFromT.typ.Kind() != reflect.Struct {
		l.disabled = true
		return l
	}
	commonFromB, ok := exactField(bType, "common", commonFromT.typ, false)
	if !ok || commonFromB.offset != 0 {
		l.disabled = true
		return l
	}

	l.tCommon = commonFromT
	l.bCommon = commonFromB
	commonType := commonFromT.typ

	l.common.mu, _ = exactField(commonType, "mu", reflect.TypeFor[sync.RWMutex](), false)
	l.common.output, _ = exactField(commonType, "output", reflect.TypeFor[[]byte](), false)
	l.common.w, _ = exactField(commonType, "w", reflect.TypeFor[io.Writer](), false)
	l.common.ran, _ = exactField(commonType, "ran", reflect.TypeFor[bool](), false)
	l.common.failed, _ = exactField(commonType, "failed", reflect.TypeFor[bool](), false)
	l.common.skipped, _ = exactField(commonType, "skipped", reflect.TypeFor[bool](), false)
	l.common.done, _ = exactField(commonType, "done", reflect.TypeFor[bool](), false)
	l.common.helperPCs, _ = exactField(commonType, "helperPCs", reflect.TypeFor[map[uintptr]struct{}](), false)
	l.common.helperNames, _ = exactField(commonType, "helperNames", reflect.TypeFor[map[string]struct{}](), false)
	l.common.cleanups, _ = exactField(commonType, "cleanups", reflect.TypeFor[[]func()](), false)
	l.common.cleanupName, _ = exactField(commonType, "cleanupName", reflect.TypeFor[string](), false)
	l.common.cleanupPc, _ = exactField(commonType, "cleanupPc", reflect.TypeFor[[]uintptr](), false)
	l.common.finished, _ = exactField(commonType, "finished", reflect.TypeFor[bool](), false)
	l.common.inFuzzFn, _ = exactField(commonType, "inFuzzFn", reflect.TypeFor[bool](), false)
	l.common.chatty, _ = wordField(commonType, "chatty", false)
	l.common.bench, _ = exactField(commonType, "bench", reflect.TypeFor[bool](), false)
	l.common.hasSub, _ = exactField(commonType, "hasSub", reflect.TypeFor[atomic.Bool](), false)
	l.common.cleanupStarted, _ = exactField(commonType, "cleanupStarted", reflect.TypeFor[atomic.Bool](), false)
	l.common.runner, _ = exactField(commonType, "runner", reflect.TypeFor[string](), false)
	l.common.isParallel, _ = exactField(commonType, "isParallel", reflect.TypeFor[bool](), false)
	l.common.parent, _ = wordField(commonType, "parent", false)
	l.common.level, _ = exactField(commonType, "level", reflect.TypeFor[int](), false)
	l.common.creator, _ = exactField(commonType, "creator", reflect.TypeFor[[]uintptr](), false)
	l.common.name, _ = exactField(commonType, "name", reflect.TypeFor[string](), false)
	l.common.start, _ = pointerSizedField(commonType, "start", false)
	l.common.duration, _ = exactField(commonType, "duration", reflect.TypeFor[time.Duration](), false)
	l.common.barrier, _ = exactField(commonType, "barrier", reflect.TypeFor[chan bool](), false)
	l.common.signal, _ = exactField(commonType, "signal", reflect.TypeFor[chan bool](), false)
	l.common.sub, _ = exactField(commonType, "sub", reflect.TypeFor[[]*testing.T](), false)
	l.common.lastRaceErrors, _ = exactField(commonType, "lastRaceErrors", reflect.TypeFor[atomic.Int64](), false)
	l.common.raceErrorLogged, _ = exactField(commonType, "raceErrorLogged", reflect.TypeFor[atomic.Bool](), false)
	l.common.tempDir, _ = exactField(commonType, "tempDir", reflect.TypeFor[string](), false)
	l.common.tempDirErr, _ = exactField(commonType, "tempDirErr", reflect.TypeFor[error](), false)
	l.common.tempDirSeq, _ = exactField(commonType, "tempDirSeq", reflect.TypeFor[int32](), false)
	l.common.isEnvSet, _ = optionalExactField(commonType, "isEnvSet", reflect.TypeFor[bool]())
	l.common.context, _ = optionalWordField(commonType, "context")
	l.common.ctx, _ = optionalExactField(commonType, "ctx", reflect.TypeFor[context.Context]())
	l.common.cancelCtx, _ = optionalExactField(commonType, "cancelCtx", reflect.TypeFor[context.CancelFunc]())
	l.common.o, _ = optionalPointerToStructField(commonType, "o")

	l.denyParallel, _ = optionalExactField(tType, "denyParallel", reflect.TypeFor[bool]())
	l.tstate, _ = optionalWordField(tType, "tstate")
	l.benchmark.benchFunc, _ = exactField(bType, "benchFunc", reflect.TypeFor[func(*testing.B)](), false)
	l.benchmark.result, _ = exactField(bType, "result", reflect.TypeFor[testing.BenchmarkResult](), false)

	l.buildOutputWriterLayout()
	l.buildContextMatcherLayout()
	l.buildChattyPrinterLayout()
	l.computeSectionFlags()
	return l
}

// buildOutputWriterLayout discovers Go 1.25+'s output writer shape when it is
// present. Older layouts leave outputWriterOK disabled and keep no-op behavior.
func (l *testingInternalsLayout) buildOutputWriterLayout() {
	if !l.common.o.available || l.common.o.typ.Kind() != reflect.Pointer || l.common.o.typ.Elem().Kind() != reflect.Struct {
		return
	}
	outputWriterType := l.common.o.typ.Elem()
	cField, cOK := pointerField(outputWriterType, "c", false)
	partialField, partialOK := exactField(outputWriterType, "partial", reflect.TypeFor[[]byte](), false)
	if !cOK || !partialOK {
		return
	}
	l.outputWriter.typ = outputWriterType
	l.outputWriter.c = cField
	l.outputWriter.partial = partialField
	l.outputWriterOK = true
}

// buildContextMatcherLayout discovers the matcher path through either the old
// context field or the newer tstate field. The current reflection code tries
// context first, so this preserves that order when both are present.
func (l *testingInternalsLayout) buildContextMatcherLayout() {
	source := l.common.context
	if !source.available {
		source = l.tstate
	}
	if !source.available || source.typ.Kind() != reflect.Pointer || source.typ.Elem().Kind() != reflect.Struct {
		return
	}

	stateType := source.typ.Elem()
	matchField, ok := wordField(stateType, "match", false)
	if !ok || matchField.typ.Kind() != reflect.Pointer || matchField.typ.Elem().Kind() != reflect.Struct {
		return
	}

	matcherType := matchField.typ.Elem()
	muField, muOK := exactField(matcherType, "mu", reflect.TypeFor[sync.Mutex](), false)
	subNamesField, subNamesOK := exactField(matcherType, "subNames", reflect.TypeFor[map[string]int32](), false)
	if !muOK || !subNamesOK {
		return
	}

	l.contextMatcher.sourceField = source
	l.contextMatcher.match = matchField
	l.contextMatcher.mu = muField
	l.contextMatcher.subNames = subNamesField
	l.contextMatcherOK = true
}

// buildChattyPrinterLayout discovers the private chattyPrinter fields used to
// capture verbose test output. Missing chatty internals disable only chatty
// instrumentation, not the rest of the offset fast paths.
func (l *testingInternalsLayout) buildChattyPrinterLayout() {
	if !l.common.chatty.available || l.common.chatty.typ.Kind() != reflect.Pointer || l.common.chatty.typ.Elem().Kind() != reflect.Struct {
		return
	}
	chattyType := l.common.chatty.typ.Elem()
	wField, wOK := exactField(chattyType, "w", reflect.TypeFor[io.Writer](), false)
	lastNameField, lastNameOK := exactField(chattyType, "lastName", reflect.TypeFor[string](), false)
	if !wOK || !lastNameOK {
		return
	}
	l.chattyPrinter.w = wField
	l.chattyPrinter.lastName = lastNameField
	l.chattyOK = true
}

// computeSectionFlags records which helpers can safely use offset access. The
// helper wrappers read these flags directly instead of repeating compatibility
// decisions in hot paths.
func (l *testingInternalsLayout) computeSectionFlags() {
	l.testFieldsOK = allAvailable(
		l.common.mu, l.common.output, l.common.level, l.common.name,
		l.common.failed, l.common.skipped, l.common.parent.unsafeField, l.common.barrier,
		l.common.signal, l.common.sub,
	)
	l.parentFieldsOK = allAvailable(
		l.common.parent.unsafeField, l.common.mu, l.common.output, l.common.level,
		l.common.name, l.common.failed, l.common.skipped, l.common.barrier,
	)
	l.createTestOK = allAvailable(l.common.barrier, l.common.signal)
	l.copyTestOK = allAvailable(
		l.common.mu, l.common.output, l.common.w, l.common.ran, l.common.failed,
		l.common.skipped, l.common.done, l.common.helperPCs, l.common.helperNames,
		l.common.cleanups, l.common.cleanupName, l.common.cleanupPc, l.common.finished,
		l.common.inFuzzFn, l.common.chatty.unsafeField, l.common.bench, l.common.hasSub,
		l.common.cleanupStarted, l.common.runner, l.common.isParallel, l.common.level,
		l.common.creator, l.common.name, l.common.start.unsafeField, l.common.duration,
		l.common.sub, l.common.lastRaceErrors, l.common.raceErrorLogged, l.common.tempDir,
		l.common.tempDirErr, l.common.tempDirSeq,
	)
	l.benchmarkFieldsOK = allAvailable(
		l.common.mu, l.common.level, l.common.name, l.common.failed,
		l.common.skipped, l.common.parent.unsafeField,
		l.benchmark.benchFunc, l.benchmark.result,
	)
}

// exactField validates that a struct contains a field with the expected type.
// When expected is nil, only existence is required.
func exactField(owner reflect.Type, name string, expected reflect.Type, optional bool) (unsafeField, bool) {
	field, ok := owner.FieldByName(name)
	if !ok {
		return unsafeField{name: name, optional: optional}, optional
	}
	if expected != nil && field.Type != expected {
		return unsafeField{name: name, optional: optional}, false
	}
	return unsafeField{
		name:      name,
		offset:    field.Offset,
		size:      field.Type.Size(),
		typ:       field.Type,
		available: true,
		optional:  optional,
	}, true
}

// optionalExactField validates an optional field. Missing optional fields are a
// successful no-op; incompatible optional fields are treated as unavailable.
func optionalExactField(owner reflect.Type, name string, expected reflect.Type) (unsafeField, bool) {
	field, ok := exactField(owner, name, expected, true)
	if !ok {
		return unsafeField{name: name, optional: true}, true
	}
	return field, true
}

// pointerField validates that a field is pointer-typed.
func pointerField(owner reflect.Type, name string, optional bool) (unsafeField, bool) {
	field, ok := owner.FieldByName(name)
	if !ok {
		return unsafeField{name: name, optional: optional}, optional
	}
	if field.Type.Kind() != reflect.Pointer {
		return unsafeField{name: name, optional: optional}, false
	}
	return unsafeField{
		name:      name,
		offset:    field.Offset,
		size:      field.Type.Size(),
		typ:       field.Type,
		available: true,
		optional:  optional,
	}, true
}

// optionalPointerToStructField validates an optional pointer-to-struct field.
func optionalPointerToStructField(owner reflect.Type, name string) (unsafeField, bool) {
	field, ok := pointerField(owner, name, true)
	if !ok || !field.available {
		return unsafeField{name: name, optional: true}, true
	}
	if field.typ.Elem().Kind() != reflect.Struct {
		return unsafeField{name: name, optional: true}, true
	}
	return field, true
}

// wordField validates a pointer-like field copied with the historical
// unsafe.Pointer semantics. Unlike start, these fields are later treated as real
// pointers, so uintptr-sized scalar fields must not enable the fast path.
func wordField(owner reflect.Type, name string, optional bool) (wordCopiedField, bool) {
	field, ok := owner.FieldByName(name)
	if !ok {
		return wordCopiedField{unsafeField: unsafeField{name: name, optional: optional}}, optional
	}
	if field.Type.Size() != unsafe.Sizeof(unsafe.Pointer(nil)) || !isPointerLikeKind(field.Type.Kind()) {
		return wordCopiedField{unsafeField: unsafeField{name: name, optional: optional}}, false
	}
	return wordCopiedField{unsafeField: unsafeField{
		name:      name,
		offset:    field.Offset,
		size:      field.Type.Size(),
		typ:       field.Type,
		available: true,
		optional:  optional,
	}}, true
}

// isPointerLikeKind reports whether a reflected field can be safely interpreted
// as one pointer word. It intentionally excludes uintptr and other scalar kinds
// even when their size matches unsafe.Pointer.
func isPointerLikeKind(kind reflect.Kind) bool {
	return kind == reflect.Pointer || kind == reflect.UnsafePointer
}

// optionalWordField validates an optional pointer-sized field.
func optionalWordField(owner reflect.Type, name string) (wordCopiedField, bool) {
	field, ok := wordField(owner, name, true)
	if !ok {
		return wordCopiedField{unsafeField: unsafeField{name: name, optional: true}}, true
	}
	return field, true
}

// pointerSizedField validates a field that must be large enough for one machine
// word. This is used for the historical partial copy of testing.common.start.
func pointerSizedField(owner reflect.Type, name string, optional bool) (wordCopiedField, bool) {
	field, ok := owner.FieldByName(name)
	if !ok {
		return wordCopiedField{unsafeField: unsafeField{name: name, optional: optional}}, optional
	}
	if field.Type.Size() < unsafe.Sizeof(unsafe.Pointer(nil)) {
		return wordCopiedField{unsafeField: unsafeField{name: name, optional: optional}}, false
	}
	return wordCopiedField{unsafeField: unsafeField{
		name:      name,
		offset:    field.Offset,
		size:      field.Type.Size(),
		typ:       field.Type,
		available: true,
		optional:  optional,
	}}, true
}

// allAvailable reports whether every required field was discovered.
func allAvailable(fields ...unsafeField) bool {
	for _, field := range fields {
		if !field.available {
			return false
		}
	}
	return true
}

// fieldRawPtr returns an unsafe pointer to a validated field.
func fieldRawPtr(base unsafe.Pointer, field unsafeField) unsafe.Pointer {
	if base == nil || !field.available {
		return nil
	}
	return unsafe.Add(base, field.offset)
}

// fieldPtr returns a typed pointer to a validated field.
func fieldPtr[T any](base unsafe.Pointer, field unsafeField) *T {
	ptr := fieldRawPtr(base, field)
	if ptr == nil {
		return nil
	}
	return (*T)(ptr)
}

// commonBaseForTest returns the embedded testing.common base for a *testing.T.
func commonBaseForTest(t *testing.T, l *testingInternalsLayout) unsafe.Pointer {
	if t == nil || l == nil || !l.tCommon.available {
		return nil
	}
	return unsafe.Add(unsafe.Pointer(t), l.tCommon.offset)
}

// commonBaseForBenchmark returns the embedded testing.common base for a
// *testing.B.
func commonBaseForBenchmark(b *testing.B, l *testingInternalsLayout) unsafe.Pointer {
	if b == nil || l == nil || !l.bCommon.available {
		return nil
	}
	return unsafe.Add(unsafe.Pointer(b), l.bCommon.offset)
}

// copyTypedField copies a typed field using normal Go assignment so pointer
// write barriers remain intact.
func copyTypedField[T any](sourceBase, targetBase unsafe.Pointer, field unsafeField) {
	*fieldPtr[T](targetBase, field) = *fieldPtr[T](sourceBase, field)
}

// copyConvertedField copies a typed field through a conversion function. It is
// used for testing.common.w, which must be wrapped in a thread-safe writer.
func copyConvertedField[T any](sourceBase, targetBase unsafe.Pointer, field unsafeField, convert func(T) T) {
	*fieldPtr[T](targetBase, field) = convert(*fieldPtr[T](sourceBase, field))
}

// copyWordField preserves the current unsafe.Pointer copy semantics for the
// small set of fields that were historically copied as one machine word.
func copyWordField(sourceBase, targetBase unsafe.Pointer, field wordCopiedField) {
	*(*unsafe.Pointer)(fieldRawPtr(targetBase, field.unsafeField)) = *(*unsafe.Pointer)(fieldRawPtr(sourceBase, field.unsafeField))
}

// pointerWord reads a pointer-sized field.
func pointerWord(base unsafe.Pointer, field wordCopiedField) unsafe.Pointer {
	ptr := fieldRawPtr(base, field.unsafeField)
	if ptr == nil {
		return nil
	}
	return *(*unsafe.Pointer)(ptr)
}

// setPrivatePointerField assigns a private pointer field using reflect.NewAt so
// reflect performs the write with the correct field type and write barrier.
func setPrivatePointerField(fieldType reflect.Type, targetPtr unsafe.Pointer, sourcePtr unsafe.Pointer) {
	sourceValue := reflect.NewAt(fieldType, unsafe.Pointer(&sourcePtr)).Elem()
	targetValue := reflect.NewAt(fieldType, targetPtr).Elem()
	targetValue.Set(sourceValue)
	runtime.KeepAlive(sourcePtr)
}
