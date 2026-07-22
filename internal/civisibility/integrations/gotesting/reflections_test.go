// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"reflect"
	"runtime"
	"sync"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
)

//dd:suite.unskippable

// TestGetFieldPointerFrom tests the getFieldPointerFrom function.
func TestGetFieldPointerFrom(t *testing.T) {
	// Create a mock struct with a private field
	mockStruct := struct {
		privateField string
	}{
		privateField: "testValue",
	}

	// Attempt to get a pointer to the private field
	ptr, err := getFieldPointerFrom(&mockStruct, "privateField")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if ptr == nil {
		t.Fatal("Expected a valid pointer, got nil")
	}
	if _, err := getFieldPointerFromWithType(&mockStruct, "privateField", reflect.TypeFor[int]()); err == nil {
		t.Fatal("Expected an error for a field with an unexpected type")
	}
	typedPtr, err := getFieldPointerFromWithType(&mockStruct, "privateField", reflect.TypeFor[string]())
	if err != nil || typedPtr == nil {
		t.Fatalf("Expected a typed field pointer, got pointer=%v error=%v", typedPtr, err)
	}

	// Dereference the pointer to get the actual value
	actualValue := (*string)(ptr)
	if *actualValue != mockStruct.privateField {
		t.Fatalf("Expected 'testValue', got %s", *actualValue)
	}

	// Modify the value through the pointer
	*actualValue = "modified value"
	if *actualValue != mockStruct.privateField {
		t.Fatalf("Expected 'modified value', got %s", mockStruct.privateField)
	}

	// Attempt to get a pointer to a non-existent field
	_, err = getFieldPointerFrom(&mockStruct, "nonExistentField")
	if err == nil {
		t.Fatal("Expected an error for non-existent field, got nil")
	}

	exerciseTestingInternalsOffsetLayout(t)
	exerciseTestingInternalsCopyEquivalence(t)
	exerciseTestingInternalsHelperMapIsolation(t)
	exerciseTestingInternalsPrivatePointerAssignment(t)
	exerciseBenchmarkFuncInstrumentationConcurrentWrites(t)
	// These pure instrumentation assertions run under this existing top-level test
	// so the subprocess span-count scenarios do not gain extra test spans.
	exerciseAdditionalFeaturePathSelection(t)
	exerciseParallelEFDSelection(t)
	exerciseMetadataOnlyPropagationSuppression(t)
	exerciseSlowEFDAbortTagging(t)
	exerciseITRCoverageBackfillState(t)
	exerciseNarrowingFlagParsing(t)
}

// TestGetInternalTestArray tests the getInternalTestArray function.
func TestGetInternalTestArray(t *testing.T) {
	assert := assert.New(t)

	// Get the internal test array from the mock testing.M
	tests := getInternalTestArray(currentM)
	assert.NotNil(tests)

	// Check that the test array contains the expected test
	testNames := make([]string, 0, len(*tests))
	for _, v := range *tests {
		testNames = append(testNames, v.Name)
		assert.NotNil(v.F)
	}

	assert.Contains(testNames, "TestGetFieldPointerFrom")
	assert.Contains(testNames, "TestGetInternalTestArray")
	assert.Contains(testNames, "TestGetInternalBenchmarkArray")
	assert.Contains(testNames, "TestCommonPrivateFields_AddLevel")
	assert.Contains(testNames, "TestGetBenchmarkPrivateFields")
}

// TestGetInternalBenchmarkArray tests the getInternalBenchmarkArray function.
func TestGetInternalBenchmarkArray(t *testing.T) {
	assert := assert.New(t)

	// Get the internal benchmark array from the mock testing.M
	benchmarks := getInternalBenchmarkArray(currentM)
	assert.NotNil(benchmarks)

	// Check that the benchmark array contains the expected benchmark
	testNames := make([]string, 0, len(*benchmarks))
	for _, v := range *benchmarks {
		testNames = append(testNames, v.Name)
		assert.NotNil(v.F)
	}

	assert.Contains(testNames, "BenchmarkDummy")
}

// TestCommonPrivateFields_AddLevel tests the AddLevel method of commonPrivateFields.
func TestCommonPrivateFields_AddLevel(t *testing.T) {
	// Create a commonPrivateFields struct with a mutex and a level
	level := 1
	commonFields := &commonPrivateFields{
		mu:    &sync.RWMutex{},
		level: &level,
	}

	// Add a level and check the new level
	newLevel := commonFields.AddLevel(1)
	if newLevel != 2 || newLevel != *commonFields.level {
		t.Fatalf("Expected level to be 2, got %d", newLevel)
	}

	// Subtract a level and check the new level
	newLevel = commonFields.AddLevel(-1)
	if newLevel != 1 || newLevel != *commonFields.level {
		t.Fatalf("Expected level to be 1, got %d", newLevel)
	}
}

// TestGetBenchmarkPrivateFields tests the getBenchmarkPrivateFields function.
func TestGetBenchmarkPrivateFields(t *testing.T) {
	// Create a new testing.B instance
	b := &testing.B{}

	// Get the private fields of the benchmark
	benchFields := getBenchmarkPrivateFields(b)
	if benchFields == nil {
		t.Fatal("Expected a valid benchmarkPrivateFields, got nil")
	}

	// Set values to the private fields
	*benchFields.name = "BenchmarkTest"
	*benchFields.level = 1
	*benchFields.benchFunc = func(_ *testing.B) {}
	*benchFields.result = testing.BenchmarkResult{}

	// Check that the private fields have the expected values
	if benchFields.level == nil || *benchFields.level != 1 {
		t.Fatalf("Expected level to be 1, got %v", *benchFields.level)
	}

	if benchFields.name == nil || *benchFields.name != b.Name() {
		t.Fatalf("Expected name to be 'BenchmarkTest', got %v", *benchFields.name)
	}

	if benchFields.benchFunc == nil {
		t.Fatal("Expected benchFunc to be set, got nil")
	}

	if benchFields.result == nil {
		t.Fatal("Expected result to be set, got nil")
	}
}

func TestShouldCaptureTerminalMessageDegradesGracefully(t *testing.T) {
	var (
		mu       sync.RWMutex
		finished bool
	)

	assert.True(t, shouldCaptureTerminalMessage(nil))
	assert.True(t, shouldCaptureTerminalMessage(&commonPrivateFields{}))
	assert.True(t, shouldCaptureTerminalMessage(&commonPrivateFields{mu: &mu}))
	assert.False(t, shouldCaptureTerminalMessage(&commonPrivateFields{mu: &mu, finished: &finished}))
	finished = true
	assert.True(t, shouldCaptureTerminalMessage(&commonPrivateFields{mu: &mu, finished: &finished}))
}

func BenchmarkDummy(*testing.B) {}

func exerciseTestingInternalsOffsetLayout(t *testing.T) {
	layout := getTestingInternalsLayout()
	if layout == nil {
		t.Fatal("expected a testing internals layout")
	}
	if layout.disabled {
		t.Fatal("expected the runtime testing layout to enable fast paths")
	}
	if layout.tCommon.offset != 0 || layout.bCommon.offset != 0 {
		t.Fatalf("expected embedded testing.common offsets to be zero, got T=%d B=%d", layout.tCommon.offset, layout.bCommon.offset)
	}
	if !layout.testFieldsOK || !layout.parentFieldsOK || !layout.copyTestOK || !layout.createTestOK || !layout.benchmarkFieldsOK {
		t.Fatalf("expected core layout sections to be enabled: %+v", layout)
	}
	if !layout.common.finished.available || !processRetryChildCleanupLayoutSupported(layout) {
		t.Fatal("expected process retry child cleanup layout to include testing.common.finished")
	}
	finishedDrift := buildTestingInternalsLayout(reflect.TypeFor[testing.T](), reflect.TypeFor[testing.B]())
	finishedDrift.common.finished.available = false
	finishedDrift.computeSectionFlags()
	if !finishedDrift.testFieldsOK || !finishedDrift.copyTestOK {
		t.Fatal("testing.common.finished drift must not disable normal in-process fast paths")
	}
	if processRetryChildCleanupLayoutSupported(finishedDrift) {
		t.Fatal("testing.common.finished drift must disable process retry child cleanup")
	}
	if fields := getTestPrivateFieldsFast(t, finishedDrift); fields == nil || fields.finished != nil {
		t.Fatal("expected finished drift to keep ordinary fields available and omit finished")
	}
	muDrift := buildTestingInternalsLayout(reflect.TypeFor[testing.T](), reflect.TypeFor[testing.B]())
	muDrift.common.mu.available = false
	muDrift.computeSectionFlags()
	if processRetryChildCleanupLayoutSupported(muDrift) {
		t.Fatal("testing.common.mu drift must disable process retry child cleanup")
	}
	driftSource := createNewTestReflect()
	driftTarget := &testing.T{}
	*getTestPrivateFieldsReflect(driftSource).name = "finished-drift"
	copyTestWithoutParentFast(driftSource, driftTarget, finishedDrift)
	if got := getTestPrivateFieldsReflect(driftTarget); got == nil || got.name == nil || *got.name != "finished-drift" {
		t.Fatal("expected copy fast path to remain valid when finished is unavailable")
	}

	invalid := buildTestingInternalsLayout(reflect.TypeFor[struct{}](), reflect.TypeFor[struct{}]())
	if invalid == nil || !invalid.disabled {
		t.Fatal("expected an invalid layout to be disabled")
	}
	if scalarWord, ok := wordField(reflect.TypeFor[struct{ parent uintptr }](), "parent", false); ok || scalarWord.available {
		t.Fatal("expected pointer-sized scalar fields to be rejected as pointer-word fields")
	}

	localT := createNewTestFast(layout)
	localFields := getTestPrivateFieldsReflect(localT)
	if localFields == nil || localFields.barrier == nil || *localFields.barrier == nil {
		t.Fatal("expected createNewTestFast to initialize barrier")
	}
	if localFields.signal == nil || *localFields.signal == nil {
		t.Fatal("expected createNewTestFast to initialize signal")
	}

	parentT := &testing.T{}
	*localFields.parent = unsafe.Pointer(parentT)
	createTestMetadata(parentT, parentT)
	defer deleteTestMetadata(parentT)
	if getTestMetadataFromPointer(*localFields.parent) == nil {
		t.Fatal("expected parent metadata lookup through parent pointer to work")
	}

	parentFields := getTestParentPrivateFieldsFast(localT, layout)
	if parentFields == nil {
		t.Fatal("expected fast parent fields")
	}
	parentFields.SetFailed(true)
	parentReflectFields := getTestPrivateFieldsReflect(parentT)
	if parentReflectFields == nil || parentReflectFields.failed == nil || !*parentReflectFields.failed {
		t.Fatal("expected fast parent fields to update parent failure state")
	}

	_ = getTestContextMatcherPrivateFieldsFast(t, layout)
	if layout.outputWriterOK {
		outputT := createNewTestReflect()
		reinitOutputWriterFast(outputT, layout)
		commonBase := commonBaseForTest(outputT, layout)
		outputWriterBase := *fieldPtr[unsafe.Pointer](commonBase, layout.common.o)
		if outputWriterBase == nil {
			t.Fatal("expected fast output writer initialization to set common.o")
		}
		if gotCommon := *fieldPtr[unsafe.Pointer](outputWriterBase, layout.outputWriter.c); gotCommon != commonBase {
			t.Fatal("expected output writer to point back to the test common")
		}
		flushOutputWriterPartialFast(outputT, layout)
	}
	if layout.chattyOK {
		_ = getTestChattyPrinterFast(t, layout)
	}
}

func exerciseTestingInternalsCopyEquivalence(t *testing.T) {
	layout := getTestingInternalsLayout()
	if layout == nil || layout.disabled || !layout.copyTestOK {
		t.Fatal("expected copy fast path to be available")
	}

	source := createNewTestReflect()
	sourceFields := getTestPrivateFieldsReflect(source)
	*sourceFields.name = "copy-equivalence"
	*sourceFields.level = 7
	*sourceFields.failed = true
	*sourceFields.skipped = true
	*sourceFields.output = []byte("copy-output")

	fastTarget := &testing.T{}
	reflectTarget := &testing.T{}
	copyTestWithoutParentFast(source, fastTarget, layout)
	copyTestWithoutParentReflect(source, reflectTarget)

	fastFields := getTestPrivateFieldsReflect(fastTarget)
	reflectFields := getTestPrivateFieldsReflect(reflectTarget)
	if *fastFields.name != *reflectFields.name ||
		*fastFields.level != *reflectFields.level ||
		*fastFields.failed != *reflectFields.failed ||
		*fastFields.skipped != *reflectFields.skipped ||
		string(*fastFields.output) != string(*reflectFields.output) {
		t.Fatal("expected fast copy to match reflection fallback for representative fields")
	}
}

func exerciseTestingInternalsHelperMapIsolation(t *testing.T) {
	layout := getTestingInternalsLayout()
	if layout == nil || layout.disabled || !layout.copyTestOK {
		t.Fatal("expected copy fast path to be available")
	}

	copyImplementations := []struct {
		name string
		copy func(source, target *testing.T)
	}{
		{
			name: "fast",
			copy: func(source, target *testing.T) {
				copyTestWithoutParentFast(source, target, layout)
			},
		},
		{name: "reflection", copy: copyTestWithoutParentReflect},
	}

	for _, implementation := range copyImplementations {
		source := createNewTestReflect()
		sourceHelperPCs, sourceHelperNames := getTestingHelperMaps(t, source)
		*sourceHelperPCs = map[uintptr]struct{}{1: {}}
		*sourceHelperNames = map[string]struct{}{"source-helper": {}}

		target := &testing.T{}
		implementation.copy(source, target)
		targetHelperPCs, targetHelperNames := getTestingHelperMaps(t, target)

		assert.Contains(t, *targetHelperPCs, uintptr(1), "%s copy did not preserve helper PCs", implementation.name)
		assert.Contains(t, *targetHelperNames, "source-helper", "%s copy did not preserve helper names", implementation.name)

		pcSentinel := uintptr(2)
		nameSentinel := implementation.name + "-helper"
		(*targetHelperPCs)[pcSentinel] = struct{}{}
		(*targetHelperNames)[nameSentinel] = struct{}{}

		assert.NotContains(t, *sourceHelperPCs, pcSentinel, "%s copy shares helper PCs with its source", implementation.name)
		assert.NotContains(t, *sourceHelperNames, nameSentinel, "%s copy shares helper names with its source", implementation.name)
	}
}

func getTestingHelperMaps(t *testing.T, test *testing.T) (*map[uintptr]struct{}, *map[string]struct{}) {
	t.Helper()
	helperPCsPointer, err := getFieldPointerFrom(test, "helperPCs")
	if err != nil {
		t.Fatalf("getting testing.common.helperPCs: %v", err)
	}
	helperNamesPointer, err := getFieldPointerFrom(test, "helperNames")
	if err != nil {
		t.Fatalf("getting testing.common.helperNames: %v", err)
	}
	return (*map[uintptr]struct{})(helperPCsPointer), (*map[string]struct{})(helperNamesPointer)
}

func exerciseTestingInternalsPrivatePointerAssignment(t *testing.T) {
	type localPrivatePointer struct {
		ptr *int
	}

	field, ok := exactField(reflect.TypeFor[localPrivatePointer](), "ptr", reflect.TypeFor[*int](), false)
	if !ok {
		t.Fatal("expected local pointer field layout")
	}

	value := 42
	target := localPrivatePointer{}
	setPrivatePointerField(field.typ, unsafe.Pointer(&target.ptr), unsafe.Pointer(&value))
	if target.ptr != &value {
		t.Fatal("expected private pointer assignment to set the target pointer")
	}

	sourceWord := struct {
		ptr unsafe.Pointer
	}{ptr: unsafe.Pointer(&value)}
	targetWord := struct {
		ptr unsafe.Pointer
	}{}
	word, ok := wordField(reflect.TypeFor[struct{ ptr unsafe.Pointer }](), "ptr", false)
	if !ok {
		t.Fatal("expected word field layout")
	}
	copyWordField(unsafe.Pointer(&sourceWord), unsafe.Pointer(&targetWord), word)
	if targetWord.ptr != sourceWord.ptr {
		t.Fatal("expected pointer-word copy to preserve the pointer value")
	}
}

// exerciseBenchmarkFuncInstrumentationConcurrentWrites verifies benchmark
// instrumentation tracking remains safe when multiple goroutines register the
// same runtime function.
func exerciseBenchmarkFuncInstrumentationConcurrentWrites(t *testing.T) {
	pc, _, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("expected caller information")
	}
	fn := runtime.FuncForPC(pc)

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 100 {
				setCiVisibilityBenchmarkFunc(fn)
			}
		})
	}
	wg.Wait()
}

func BenchmarkGetTestPrivateFields(b *testing.B) {
	t := createNewTest()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = getTestPrivateFields(t)
	}
}

func BenchmarkGetTestParentPrivateFields(b *testing.B) {
	t := createNewTest()
	parent := &testing.T{}
	fields := getTestPrivateFields(t)
	*fields.parent = unsafe.Pointer(parent)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = getTestParentPrivateFields(t)
	}
}

func BenchmarkCopyTestWithoutParent(b *testing.B) {
	source := createNewTest()
	target := &testing.T{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		copyTestWithoutParent(source, target)
	}
}

func BenchmarkCreateNewTest(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = createNewTest()
	}
}
