// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/coverage"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/impactedtests"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

const (
	processRetrySubtreeVersion        = 1
	processRetrySubtreeReason         = "quarantined_race"
	processRetrySubtreeMaxDirectives  = 4 * 1024
	processRetrySubtreeMaxResults     = 4 * 1024
	processRetrySubtreeResultMaxBytes = 16 * 1024 * 1024
	processRetrySubtreeOutputMaxBytes = 8 * 1024
	processRetrySubtreeWireMaxBytes   = 16 * 1024 * 1024
)

func processRetryWireMaxBytes(subtree bool) int {
	if subtree {
		return processRetrySubtreeWireMaxBytes
	}
	return processRetryResultMaxBytes
}

// quarantinedRaceProcessContext is created only for the explicit process
// backend. It is carried through test metadata so dynamically discovered
// subtests can use the M-scoped launch snapshot without a package-global
// scheduler.
type quarantinedRaceProcessContext struct {
	launchTemplate         *processRetryLaunchBaseline
	mRunEpoch              uint64
	invocations            *atomic.Uint64
	attemptToFixRetries    int
	collectPerTestCoverage bool
	collectAggregate       bool
}

type processRetrySubtreeConfig struct {
	Version              int                            `json:"version"`
	SelectedRoot         string                         `json:"selected_root"`
	Root                 processRetrySubtreeDirective   `json:"root"`
	Directives           []processRetrySubtreeDirective `json:"directives,omitempty"`
	ITR                  []processRetrySubtreeITR       `json:"itr,omitempty"`
	AttemptToFixRetries  int                            `json:"attempt_to_fix_retries,omitempty"`
	OwnsAttemptToFix     bool                           `json:"owns_attempt_to_fix,omitempty"`
	AncestorAttemptToFix bool                           `json:"ancestor_attempt_to_fix,omitempty"`
	CollectPerTest       bool                           `json:"collect_per_test_coverage,omitempty"`
	CollectAggregate     bool                           `json:"collect_aggregate_coverage,omitempty"`
	ITRCoverageActive    bool                           `json:"itr_coverage_active,omitempty"`
	ImpactedTestsEnabled bool                           `json:"impacted_tests_enabled,omitempty"`
}

type processRetrySubtreeDirective struct {
	TestName     string `json:"test_name"`
	Disabled     bool   `json:"disabled,omitempty"`
	Quarantined  bool   `json:"quarantined,omitempty"`
	AttemptToFix bool   `json:"attempt_to_fix,omitempty"`
	Modified     bool   `json:"modified,omitempty"`
}

type processRetrySubtreeITR struct {
	TestName                string `json:"test_name"`
	MissingLineCodeCoverage bool   `json:"missing_line_code_coverage,omitempty"`
}

type processRetryTestSource struct {
	RuntimePath      string `json:"runtime_path,omitempty"`
	RuntimeStartLine int    `json:"runtime_start_line,omitempty"`
	RuntimeEndLine   int    `json:"runtime_end_line,omitempty"`
	Unskippable      bool   `json:"unskippable,omitempty"`
}

// processRetrySubtreeResult is the event-shaped payload returned for every
// descendant of the selected root. The envelope fields in processRetryResult
// use the same status contract for the root itself.
type processRetrySubtreeResult struct {
	TestName        string                             `json:"test_name"`
	Status          processRetryStatus                 `json:"status"`
	StartUnixNano   int64                              `json:"start_unix_nano"`
	FinishUnixNano  int64                              `json:"finish_unix_nano"`
	DurationNanos   int64                              `json:"duration_nanos"`
	Failed          bool                               `json:"failed"`
	Skipped         bool                               `json:"skipped"`
	Panic           bool                               `json:"panic"`
	RaceDetected    bool                               `json:"race_detected,omitempty"`
	Disabled        bool                               `json:"disabled,omitempty"`
	Quarantined     bool                               `json:"quarantined,omitempty"`
	AttemptToFix    bool                               `json:"attempt_to_fix,omitempty"`
	AttemptToFixOwn bool                               `json:"attempt_to_fix_owner,omitempty"`
	SkippedByITR    bool                               `json:"skipped_by_itr,omitempty"`
	ITRForcedRun    bool                               `json:"itr_forced_run,omitempty"`
	Modified        bool                               `json:"modified,omitempty"`
	ErrorType       string                             `json:"error_type,omitempty"`
	ErrorMessage    string                             `json:"error_message,omitempty"`
	ErrorStack      string                             `json:"error_stack,omitempty"`
	SkipReason      string                             `json:"skip_reason,omitempty"`
	OutputTail      string                             `json:"output_tail,omitempty"`
	OutputTruncated bool                               `json:"output_truncated,omitempty"`
	Source          *processRetryTestSource            `json:"source,omitempty"`
	Coverage        []coverage.ProcessTestCoverageFile `json:"coverage,omitempty"`
	order           uint64
}

func newQuarantinedRaceProcessContext(
	launchTemplate *processRetryLaunchBaseline,
	mRunEpoch uint64,
	invocations *atomic.Uint64,
	attemptToFixRetries int,
) *quarantinedRaceProcessContext {
	if invocations == nil {
		return nil
	}
	return &quarantinedRaceProcessContext{
		launchTemplate:         launchTemplate,
		mRunEpoch:              mRunEpoch,
		invocations:            invocations,
		attemptToFixRetries:    max(attemptToFixRetries, 0),
		collectPerTestCoverage: coverage.CanCollectPerTestCoverage(),
		collectAggregate:       coverage.CanComputeCoverageProfile(),
	}
}

func buildProcessRetrySubtreeConfig(
	ctx *quarantinedRaceProcessContext,
	testInfo *commonInfo,
	execMeta *testExecutionMetadata,
	parentExecMeta *testExecutionMetadata,
) (*processRetrySubtreeConfig, error) {
	if ctx == nil || testInfo == nil || execMeta == nil || execMeta.identity == nil {
		return nil, errors.New("missing quarantined race process context")
	}
	identity := execMeta.identity
	cfg := &processRetrySubtreeConfig{
		Version:             processRetrySubtreeVersion,
		SelectedRoot:        identity.FullName,
		Root:                processRetryDirectiveFromMetadata(identity.FullName, execMeta),
		AttemptToFixRetries: ctx.attemptToFixRetries,
		OwnsAttemptToFix:    execMeta.isAttemptToFix && execMeta.shouldOrchestrateAttemptToFix,
		CollectPerTest:      ctx.collectPerTestCoverage,
		CollectAggregate:    ctx.collectAggregate,
	}
	subtestFeaturesEnabled := false
	if settings := integrations.GetSettings(); settings != nil {
		subtestFeaturesEnabled = settings.SubtestFeaturesEnabled
		cfg.ImpactedTestsEnabled = settings.ImpactedTestsEnabled
		if cfg.ImpactedTestsEnabled {
			cfg.Root.Modified = cfg.Root.Modified || integrations.IsTestFuncModified(testInfo.testName, testInfo.sourceFunc)
		}
	}
	if parentExecMeta != nil && parentExecMeta.isAttemptToFix {
		cfg.AncestorAttemptToFix = true
	}

	if data := integrations.GetTestManagementTestsData(); subtestFeaturesEnabled && data != nil {
		if module, ok := data.Modules[identity.ModuleName]; ok {
			if suite, ok := module.Suites[identity.SuiteName]; ok {
				for name, properties := range suite.Tests {
					if strings.HasPrefix(name, cfg.SelectedRoot+"/") {
						cfg.Directives = append(cfg.Directives, processRetrySubtreeDirective{
							TestName:     name,
							Disabled:     properties.Properties.Disabled,
							Quarantined:  properties.Properties.Quarantined,
							AttemptToFix: properties.Properties.AttemptToFix,
						})
					}
				}
			}
		}
	}
	slices.SortFunc(cfg.Directives, func(a, b processRetrySubtreeDirective) int {
		return strings.Compare(a.TestName, b.TestName)
	})

	itr := currentITRState()
	if itr != nil {
		cfg.ITRCoverageActive = itr.coverageActive
		if itr.response != nil {
			if tests, ok := itr.response.Skippables[identity.SuiteName]; ok {
				for name, candidates := range tests {
					if name != cfg.SelectedRoot && !strings.HasPrefix(name, cfg.SelectedRoot+"/") {
						continue
					}
					entry := processRetrySubtreeITR{TestName: name}
					matched := false
					for _, candidate := range candidates {
						if strings.TrimSpace(candidate.Parameters) != "" {
							continue
						}
						matched = true
						entry.MissingLineCodeCoverage = entry.MissingLineCodeCoverage || candidate.MissingLineCodeCoverage
					}
					if matched {
						cfg.ITR = append(cfg.ITR, entry)
					}
				}
			}
		}
	}
	slices.SortFunc(cfg.ITR, func(a, b processRetrySubtreeITR) int {
		return strings.Compare(a.TestName, b.TestName)
	})
	if err := validateProcessRetrySubtreeConfig(cfg, cfg.SelectedRoot); err != nil {
		return nil, err
	}
	return cfg, nil
}

func processRetryDirectiveFromMetadata(name string, meta *testExecutionMetadata) processRetrySubtreeDirective {
	if meta == nil {
		return processRetrySubtreeDirective{TestName: name}
	}
	return processRetrySubtreeDirective{
		TestName:     name,
		Disabled:     meta.isDisabled,
		Quarantined:  meta.isQuarantined,
		AttemptToFix: meta.isAttemptToFix,
		Modified:     meta.isAModifiedTest,
	}
}

func validateProcessRetrySubtreeConfig(cfg *processRetrySubtreeConfig, selectedRoot string) error {
	if cfg == nil {
		return nil
	}
	if cfg.Version != processRetrySubtreeVersion || cfg.SelectedRoot != selectedRoot ||
		strings.TrimSpace(cfg.SelectedRoot) == "" || cfg.Root.TestName != cfg.SelectedRoot ||
		!cfg.Root.Quarantined || cfg.AttemptToFixRetries < 0 ||
		len(cfg.Directives) > processRetrySubtreeMaxDirectives || len(cfg.ITR) > processRetrySubtreeMaxDirectives {
		return errors.New("invalid process retry subtree configuration")
	}
	if cfg.OwnsAttemptToFix && !cfg.Root.AttemptToFix || cfg.AncestorAttemptToFix && cfg.OwnsAttemptToFix {
		return errors.New("invalid process retry subtree attempt ownership")
	}
	previous := ""
	for _, directive := range cfg.Directives {
		if directive.TestName == cfg.SelectedRoot || !processRetryNameWithinRoot(directive.TestName, cfg.SelectedRoot) || directive.TestName <= previous {
			return errors.New("invalid process retry subtree directive")
		}
		previous = directive.TestName
	}
	previous = ""
	for _, candidate := range cfg.ITR {
		if !processRetryNameWithinRoot(candidate.TestName, cfg.SelectedRoot) || candidate.TestName <= previous {
			return errors.New("invalid process retry subtree ITR directive")
		}
		previous = candidate.TestName
	}
	return nil
}

func processRetryNameWithinRoot(name, root string) bool {
	return name == root || strings.HasPrefix(name, root+"/")
}

func processRetryExactRunPattern(fullName string) string {
	segments := strings.Split(fullName, "/")
	for idx := range segments {
		segments[idx] = "^" + regexp.QuoteMeta(segments[idx]) + "$"
	}
	return strings.Join(segments, "/")
}

func (cfg *processRetrySubtreeConfig) exactDirective(name string) (processRetrySubtreeDirective, bool) {
	idx, ok := slices.BinarySearchFunc(cfg.Directives, name, func(d processRetrySubtreeDirective, name string) int {
		return strings.Compare(d.TestName, name)
	})
	if !ok {
		return processRetrySubtreeDirective{}, false
	}
	return cfg.Directives[idx], true
}

func (cfg *processRetrySubtreeConfig) resolveDirective(name string) (processRetrySubtreeDirective, string) {
	resolved := cfg.Root
	resolved.TestName = name
	attemptOwner := ""
	if cfg.OwnsAttemptToFix {
		attemptOwner = cfg.SelectedRoot
	}
	if name == cfg.SelectedRoot {
		return resolved, attemptOwner
	}
	rootParts := strings.Split(cfg.SelectedRoot, "/")
	parts := strings.Split(name, "/")
	for idx := len(rootParts) + 1; idx <= len(parts); idx++ {
		candidate := strings.Join(parts[:idx], "/")
		exact, ok := cfg.exactDirective(candidate)
		if !ok {
			continue
		}
		resolved.Disabled = resolved.Disabled || exact.Disabled
		resolved.Quarantined = resolved.Quarantined || exact.Quarantined
		if exact.AttemptToFix {
			if !resolved.AttemptToFix {
				attemptOwner = candidate
			}
			resolved.AttemptToFix = true
		} else {
			resolved.AttemptToFix = false
			attemptOwner = ""
		}
	}
	return resolved, attemptOwner
}

func (cfg *processRetrySubtreeConfig) itrDecision(name string, meta processRetrySubtreeDirective, sourceFunc *runtime.Func) (skip, forced bool) {
	idx, ok := slices.BinarySearchFunc(cfg.ITR, name, func(candidate processRetrySubtreeITR, name string) int {
		return strings.Compare(candidate.TestName, name)
	})
	if !ok || meta.AttemptToFix || meta.Modified {
		return false, false
	}
	_, _, unskippable := integrations.TestFuncSourceMetadata(sourceFunc)
	if unskippable {
		return false, true
	}
	if cfg.ITRCoverageActive && cfg.ITR[idx].MissingLineCodeCoverage {
		return false, false
	}
	return true, false
}

func processRetrySourceFromFunc(fn *runtime.Func) *processRetryTestSource {
	if fn == nil {
		return nil
	}
	path, line := fn.FileLine(fn.Entry())
	if path == "" || line <= 0 {
		return nil
	}
	startLine, endLine, unskippable := integrations.TestFuncSourceMetadata(fn)
	if startLine <= 0 {
		startLine = line
	}
	return &processRetryTestSource{
		RuntimePath: path, RuntimeStartLine: startLine, RuntimeEndLine: endLine,
		Unskippable: unskippable,
	}
}

func truncateProcessRetrySubtreeOutput(output []byte) (string, bool) {
	return truncateProcessRetrySubtreeOutputTo(string(output), processRetrySubtreeOutputMaxBytes)
}

func truncateProcessRetrySubtreeOutputTo(output string, maxBytes int) (string, bool) {
	maxBytes = max(maxBytes, 0)
	truncated := len(output) > maxBytes
	if truncated {
		output = output[len(output)-maxBytes:]
	}
	normalized := strings.ToValidUTF8(output, "\uFFFD")
	if processRetryJSONStringFits(normalized, maxBytes) {
		return normalized, truncated
	}
	runes := []rune(normalized)
	low, high := 0, len(runes)
	for low < high {
		mid := low + (high-low+1)/2
		if processRetryJSONStringFits(string(runes[len(runes)-mid:]), maxBytes) {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[len(runes)-low:]), true
}

type quarantinedRaceChildState struct {
	cfg             *processRetrySubtreeConfig
	mu              locking.Mutex
	next            atomic.Uint64
	results         []processRetrySubtreeResult
	impacted        *impactedtests.ImpactedTestAnalyzer
	aggregateOnce   sync.Once
	beginAggregate  func()
	aggregateFinish sync.Once
	finishAggregate func()
	pauseAggregate  func() func()
	parallelBridge  func() error
	parallelOnce    sync.Once
	parallelStarted atomic.Bool
	parallelDone    chan error
	coverage        quarantinedRaceCoverageCoordinator
}

type quarantinedRaceCoverageInterval struct {
	id    uint64
	name  string
	valid bool
}

// quarantinedRaceCoverageCoordinator never serializes user code. Runtime
// coverage counters are process-global, so overlapping sibling branches cannot
// be attributed safely; their per-test deltas are discarded instead. Nested
// collectors remain valid because their inclusive overlap is deterministic.
type quarantinedRaceCoverageCoordinator struct {
	mu     sync.Mutex
	next   uint64
	active map[uint64]*quarantinedRaceCoverageInterval
}

func (c *quarantinedRaceCoverageCoordinator) begin(name string) *quarantinedRaceCoverageInterval {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		c.active = make(map[uint64]*quarantinedRaceCoverageInterval)
	}
	c.next++
	interval := &quarantinedRaceCoverageInterval{id: c.next, name: name, valid: true}
	for _, current := range c.active {
		if processRetryNameWithinRoot(name, current.name) || processRetryNameWithinRoot(current.name, name) {
			continue
		}
		current.valid = false
		interval.valid = false
	}
	c.active[interval.id] = interval
	return interval
}

func (c *quarantinedRaceCoverageCoordinator) finish(interval *quarantinedRaceCoverageInterval) bool {
	if interval == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.active, interval.id)
	return interval.valid
}

func newQuarantinedRaceChildState(cfg *processRetrySubtreeConfig) *quarantinedRaceChildState {
	state := &quarantinedRaceChildState{cfg: cfg, parallelDone: make(chan error, 1)}
	if cfg != nil && cfg.ImpactedTestsEnabled {
		state.impacted, _ = impactedtests.NewImpactedTestAnalyzer()
	}
	return state
}

func (s *quarantinedRaceChildState) begin() (uint64, time.Time, int64) {
	return s.next.Add(1), time.Now(), retryAttemptRaceErrors()
}

func (s *quarantinedRaceChildState) beginAggregateCoverage(name string) {
	if s == nil || s.cfg == nil || name != s.cfg.SelectedRoot || s.beginAggregate == nil {
		return
	}
	s.aggregateOnce.Do(s.beginAggregate)
}

func (s *quarantinedRaceChildState) finishAggregateCoverage(name string) {
	if s == nil || s.cfg == nil || name != s.cfg.SelectedRoot || s.finishAggregate == nil {
		return
	}
	s.aggregateFinish.Do(s.finishAggregate)
}

func (s *quarantinedRaceChildState) append(result processRetrySubtreeResult) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.results = append(s.results, result)
	s.mu.Unlock()
}

func (s *quarantinedRaceChildState) snapshot() []processRetrySubtreeResult {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	results := append([]processRetrySubtreeResult(nil), s.results...)
	s.mu.Unlock()
	slices.SortFunc(results, func(a, b processRetrySubtreeResult) int {
		return cmp.Compare(a.order, b.order)
	})
	for idx := range results {
		results[idx].order = 0
	}
	return results
}

func (s *quarantinedRaceChildState) startParallelBridge() {
	if s == nil || s.parallelBridge == nil {
		return
	}
	s.parallelOnce.Do(func() {
		s.parallelStarted.Store(true)
		// Do not hold up native Parallel: it must first release the selected
		// test's child-process parent before the parent-process bridge can resume.
		go func() {
			s.parallelDone <- s.parallelBridge()
		}()
	})
}

func (s *quarantinedRaceChildState) waitParallelBridge() error {
	if s == nil || !s.parallelStarted.Load() {
		return nil
	}
	return <-s.parallelDone
}

func processRetrySubtreeCoveragePath(resultPath string) string {
	return resultPath + ".coverage.out"
}

func (o *processRetryChildObservation) buildSubtreeResult(result processRetryResult, controlled processRetryStatus) processRetryResult {
	if o.aggregateErr != nil {
		return processRetryNotRunResult(o.cfg, "coverage_finalization_failed")
	}
	if o.aggregate != nil {
		if err := o.aggregate.WriteDelta(processRetrySubtreeCoveragePath(o.cfg.ResultPath)); err != nil {
			return processRetryNotRunResult(o.cfg, "coverage_finalization_failed")
		}
	}
	results := o.subtree.snapshot()
	topLevelName, _ := topLevelTestName(o.cfg.TestName)
	if o.cfg.Subtree.SelectedRoot == topLevelName {
		result.Source = o.source
		result.SkippedByITR = o.execMeta.isItrSkipped
		result.ITRForcedRun = o.execMeta.isItrForcedRun
		result.Modified = o.execMeta.isAModifiedTest
		if o.coverage != nil {
			result.Coverage = o.coverage.Finish()
		}
		result.Subtests = results
		result.OutputTail, result.OutputTruncated = truncateProcessRetrySubtreeOutput(o.result.output)
		applyProcessRetryControlledSubtreeStatus(&result, controlled)
		return result
	}
	rootIndex := -1
	for idx := range results {
		if results[idx].TestName == o.cfg.Subtree.SelectedRoot {
			rootIndex = idx
			break
		}
	}
	if rootIndex < 0 {
		return processRetryNotRunResult(o.cfg, "selected_root_not_run")
	}
	root := results[rootIndex]
	result.Status = root.Status
	result.StartUnixNano = root.StartUnixNano
	result.FinishUnixNano = root.FinishUnixNano
	result.DurationNanos = root.DurationNanos
	result.Failed = root.Failed
	result.Skipped = root.Skipped
	result.Panic = root.Panic
	result.RaceDetected = root.RaceDetected
	result.ErrorType = root.ErrorType
	result.ErrorMessage = root.ErrorMessage
	result.ErrorStack = root.ErrorStack
	result.SkipReason = root.SkipReason
	result.OutputTail = root.OutputTail
	result.OutputTruncated = root.OutputTruncated
	result.Source = root.Source
	result.Coverage = root.Coverage
	result.SkippedByITR = root.SkippedByITR
	result.ITRForcedRun = root.ITRForcedRun
	result.Modified = root.Modified
	result.Subtests = append(results[:rootIndex:rootIndex], results[rootIndex+1:]...)
	// A controlled terminal on the top-level carrier belongs to an ancestor of
	// this nested root; the copied root keeps its independently recorded status.
	return result
}

func applyProcessRetryControlledSubtreeStatus(result *processRetryResult, controlled processRetryStatus) {
	if result == nil || controlled == "" {
		return
	}
	result.Status = controlled
	result.Failed = true
	result.Panic = true
	result.Skipped = false
	result.SkipReason = ""
}

func runQuarantinedRaceChildSubtest(t *testing.T, original func(*testing.T), parent *testExecutionMetadata) {
	state := parent.quarantinedRaceChild
	if state == nil || state.cfg == nil {
		original(t)
		return
	}
	name := t.Name()
	execMeta := createTestMetadata(t, nil)
	execMeta.test = parent.test
	execMeta.processRetryOwner = parent
	execMeta.quarantinedRaceChild = state
	if !processRetryNameWithinRoot(name, state.cfg.SelectedRoot) {
		defer deleteTestMetadata(t)
		original(t)
		return
	}
	state.beginAggregateCoverage(name)

	directive, attemptOwner := state.cfg.resolveDirective(name)
	execMeta.identity = newTestIdentity("", "", name)
	execMeta.isDisabled = directive.Disabled
	execMeta.isQuarantined = directive.Quarantined
	execMeta.isAttemptToFix = directive.AttemptToFix
	execMeta.isItrForcedRun = false
	execMeta.hasExplicitDisabled = true
	execMeta.hasExplicitQuarantined = true
	execMeta.hasExplicitAttemptToFix = true
	execMeta.shouldOrchestrateAttemptToFix = attemptOwner == name

	sourceFunc := runtime.FuncForPC(reflect.ValueOf(original).Pointer())
	if testifyData := getTestifyTest(t); testifyData != nil && testifyData.methodFunc != nil {
		sourceFunc = testifyData.methodFunc
	}
	source := processRetrySourceFromFunc(sourceFunc)
	execMeta.isAModifiedTest = directive.Modified || integrations.IsTestFuncModifiedWithAnalyzer(state.impacted, name, sourceFunc)
	directive.Modified = execMeta.isAModifiedTest
	order, start, raceBaseline := state.begin()
	activeStart := start
	activeDuration := time.Duration(0)
	restoreChatty := bufferQuarantinedRaceChildOutput(t)
	var collector *coverage.ProcessTestCoverage
	var coverageInterval *quarantinedRaceCoverageInterval
	coverageValid := true
	collectCoverage := state.cfg.CollectPerTest && coverage.CanCollect()
	if collectCoverage {
		coverageInterval = state.coverage.begin(name)
		if source != nil {
			collector = coverage.BeginProcessTestCoverage(source.RuntimePath)
		}
	}
	execMeta.processRetryParallelPause = func() func() {
		activeDuration += time.Since(activeStart)
		activeStart = time.Time{}
		// A nested selected root releases its discovery ancestor in Parallel.
		// Exclude that ancestor's work from this root's aggregate profile.
		var resumeAggregate func()
		if name == state.cfg.SelectedRoot && state.pauseAggregate != nil {
			resumeAggregate = state.pauseAggregate()
		}
		if collector != nil {
			collector.Pause()
		}
		coverageValid = state.coverage.finish(coverageInterval) && coverageValid
		coverageInterval = nil
		return func() {
			activeStart = time.Now()
			if resumeAggregate != nil {
				resumeAggregate()
			}
			if collectCoverage {
				coverageInterval = state.coverage.begin(name)
			}
			if collector != nil {
				collector.Resume()
			}
		}
	}

	bodyReturned := false
	defer func() {
		panicData := recover()
		unexpected := panicData == nil && processRetryUnexpectedTestTermination(t, bodyReturned)
		if panicData != nil || unexpected {
			if panicData == nil {
				panicData = unexpectedTestTerminationMessage
			}
			t.Fail()
			execMeta.processRetryPanic.CompareAndSwap(nil, &processRetryErrorInfo{
				Type:    "panic",
				Message: truncateProcessRetryErrorMessage(toString(panicData)),
				Stack:   truncateProcessRetryErrorStack(utils.GetStacktrace(1)),
			})
		}

		// Drain user cleanups before snapshotting. The helper also releases and
		// waits for queued parallel descendants, so the selected root includes
		// their failures, timing, and coverage. A regular t.Cleanup finalizer would
		// run after testing has already classified a cleanup panic.
		cleanup := &testCleanupResult{}
		runTestCleanupWithOptions(t, cleanup, true)
		if cleanup.panicData != nil {
			t.Fail()
			execMeta.processRetryPanic.CompareAndSwap(nil, &processRetryErrorInfo{
				Type:    "panic",
				Message: truncateProcessRetryErrorMessage(toString(cleanup.panicData)),
				Stack:   truncateProcessRetryErrorStack(cleanup.panicStacktrace),
			})
			// The parent replays this recorded panic. Re-panicking here would let
			// testing terminate the child before it can write the subtree result.
		}
		state.finishAggregateCoverage(name)

		if !activeStart.IsZero() {
			activeDuration += time.Since(activeStart)
			activeStart = time.Time{}
		}
		files := collector.Finish()
		coverageValid = state.coverage.finish(coverageInterval) && coverageValid
		if !coverageValid {
			files = nil
		}
		restoreChatty()
		finish := start.Add(activeDuration)
		fields := getTestPrivateFields(t)
		result := processRetrySubtreeResult{
			TestName:        name,
			StartUnixNano:   start.UnixNano(),
			FinishUnixNano:  finish.UnixNano(),
			DurationNanos:   activeDuration.Nanoseconds(),
			Failed:          t.Failed(),
			Skipped:         t.Skipped(),
			RaceDetected:    processRetrySubtreeRaceDetected(fields, raceBaseline, retryAttemptRaceErrors()),
			Disabled:        directive.Disabled,
			Quarantined:     directive.Quarantined,
			AttemptToFix:    directive.AttemptToFix,
			AttemptToFixOwn: attemptOwner == name,
			ITRForcedRun:    execMeta.isItrForcedRun,
			SkippedByITR:    execMeta.isItrSkipped,
			Modified:        execMeta.isAModifiedTest,
			Source:          source,
			Coverage:        files,
			order:           order,
		}
		if fields != nil {
			result.OutputTail, result.OutputTruncated = truncateProcessRetrySubtreeOutput(fields.GetOutput())
		}
		if panicInfo := execMeta.processRetryPanic.Load(); panicInfo != nil {
			result.Panic = true
			result.ErrorType = panicInfo.Type
			result.ErrorMessage = panicInfo.Message
			result.ErrorStack = panicInfo.Stack
		} else if errorInfo := execMeta.processRetryError.Load(); errorInfo != nil {
			result.ErrorType = errorInfo.Type
			result.ErrorMessage = errorInfo.Message
			result.ErrorStack = errorInfo.Stack
		}
		if skipReason := execMeta.processRetrySkipReason.Load(); skipReason != nil && result.Skipped && !result.Failed {
			result.SkipReason = *skipReason
		}
		if result.RaceDetected || result.Panic {
			result.Failed = true
		}
		switch {
		case result.Failed:
			result.Status = processRetryStatusFail
			result.Skipped = false
			result.SkipReason = ""
		case result.Skipped:
			result.Status = processRetryStatusSkip
		default:
			result.Status = processRetryStatusPass
		}
		state.append(result)
		if unexpected && fields != nil && fields.mu != nil && fields.finished != nil {
			// runtime.Goexit bypasses the normal return to testing.tRunner. The
			// subtree result is committed, so consume that terminal before the
			// native runner can turn it into a process-ending panic.
			fields.mu.Lock()
			*fields.finished = true
			fields.mu.Unlock()
		}
		deleteTestMetadata(t)

		// Body panics are valid test results in this child protocol. They were
		// recorded above and t.Fail propagated them to the selected root; re-panic
		// would let testing terminate the process before it can write the result.
	}()

	if directive.Disabled && !directive.AttemptToFix {
		reason := constants.TestDisabledSkipReason
		execMeta.processRetrySkipReason.Store(&reason)
		t.SkipNow()
	}
	if skip, forced := state.cfg.itrDecision(name, directive, sourceFunc); skip {
		execMeta.isItrSkipped = true
		reason := constants.SkippedByITRReason
		execMeta.processRetrySkipReason.Store(&reason)
		t.SkipNow()
	} else {
		execMeta.isItrForcedRun = forced
	}

	original(t)
	bodyReturned = true
}

func processRetrySubtreeRaceDetected(fields *commonPrivateFields, initialBaseline, current int64) bool {
	if fields != nil && fields.raceErrorLogged != nil && fields.raceErrorLogged.Load() {
		// Parallel checks and records races before replacing lastRaceErrors with
		// its post-resume baseline. Keep that already-attributed race.
		return true
	}
	baseline := initialBaseline
	if fields != nil && fields.lastRaceErrors != nil {
		// testing advances this baseline on the reporting test and its ancestors;
		// Parallel also resets it after resume. In both cases, only newer races
		// belong to this test unless raceErrorLogged already records its own race.
		baseline = fields.lastRaceErrors.Load()
	}
	return current > baseline
}

func bufferQuarantinedRaceChildOutput(t *testing.T) func() {
	layout := getTestingInternalsLayout()
	if t == nil || layout == nil || layout.disabled || !layout.chattyOK {
		return func() {}
	}
	base := commonBaseForTest(t, layout)
	if base == nil {
		return func() {}
	}
	mu := fieldPtr[sync.RWMutex](base, layout.common.mu)
	mu.Lock()
	chatty := pointerWord(base, layout.common.chatty)
	if chatty != nil {
		setPrivatePointerField(layout.common.chatty.typ, fieldRawPtr(base, layout.common.chatty.unsafeField), nil)
	}
	mu.Unlock()
	return func() {
		if chatty == nil {
			return
		}
		mu.Lock()
		setPrivatePointerField(layout.common.chatty.typ, fieldRawPtr(base, layout.common.chatty.unsafeField), chatty)
		mu.Unlock()
		runtime.KeepAlive(t)
	}
}

func validateProcessRetrySubtreeResultEnvelope(result processRetryResult, expected processRetryChildConfig) error {
	if expected.Subtree == nil {
		if result.OutputTail != "" || result.OutputTruncated || result.Source != nil || len(result.Coverage) > 0 || len(result.Subtests) > 0 ||
			result.SkippedByITR || result.ITRForcedRun || result.Modified {
			return fmt.Errorf("%w: unexpected subtree data", errProcessRetryResultInvalid)
		}
		return nil
	}
	if result.Status == processRetryStatusNotRun {
		return nil
	}
	if result.StartUnixNano == 0 || result.FinishUnixNano < result.StartUnixNano ||
		result.DurationNanos != result.FinishUnixNano-result.StartUnixNano {
		return fmt.Errorf("%w: invalid subtree root timing (%d,%d,%d)", errProcessRetryResultInvalid, result.StartUnixNano, result.FinishUnixNano, result.DurationNanos)
	}
	if result.OutputTruncated && result.OutputTail == "" {
		return fmt.Errorf("%w: invalid subtree root output", errProcessRetryResultInvalid)
	}
	if result.SkippedByITR && (result.ITRForcedRun || result.Status != processRetryStatusSkip || result.SkipReason != constants.SkippedByITRReason) {
		return fmt.Errorf("%w: invalid subtree root ITR mirrors", errProcessRetryResultInvalid)
	}
	if err := validateProcessRetryTestSource(result.Source); err != nil {
		return err
	}
	if err := validateProcessRetryCoverageFiles(result.Coverage); err != nil {
		return err
	}
	if len(result.Subtests) > processRetrySubtreeMaxResults {
		return fmt.Errorf("%w: too many subtree results", errProcessRetryResultInvalid)
	}
	seen := make(map[string]struct{}, len(result.Subtests))
	for _, subtest := range result.Subtests {
		if subtest.TestName == expected.Subtree.SelectedRoot || !processRetryNameWithinRoot(subtest.TestName, expected.Subtree.SelectedRoot) {
			return fmt.Errorf("%w: subtree identity outside selected root", errProcessRetryResultInvalid)
		}
		if _, duplicate := seen[subtest.TestName]; duplicate {
			return fmt.Errorf("%w: duplicate subtree result", errProcessRetryResultInvalid)
		}
		seen[subtest.TestName] = struct{}{}
		if err := validateProcessRetrySubtreeResult(subtest, expected.Subtree); err != nil {
			return err
		}
	}
	return nil
}

func validateProcessRetrySubtreeResult(result processRetrySubtreeResult, cfg *processRetrySubtreeConfig) error {
	if result.StartUnixNano == 0 || result.FinishUnixNano < result.StartUnixNano ||
		result.DurationNanos != result.FinishUnixNano-result.StartUnixNano {
		return fmt.Errorf("%w: invalid subtree timing", errProcessRetryResultInvalid)
	}
	if !processRetryJSONStringFits(result.ErrorType, processRetryErrorTypeMaxBytes) ||
		!processRetryJSONStringFits(result.ErrorMessage, processRetryErrorMessageMaxBytes) ||
		!processRetryJSONStringFits(result.ErrorStack, processRetryErrorStackMaxBytes) ||
		!processRetryJSONStringFits(result.SkipReason, processRetrySkipReasonMaxBytes) ||
		!processRetryJSONStringFits(result.OutputTail, processRetrySubtreeOutputMaxBytes) {
		return fmt.Errorf("%w: subtree metadata field too large", errProcessRetryResultInvalid)
	}
	if result.OutputTruncated && result.OutputTail == "" {
		return fmt.Errorf("%w: invalid subtree output", errProcessRetryResultInvalid)
	}
	if err := validateProcessRetryTestSource(result.Source); err != nil {
		return err
	}
	if err := validateProcessRetryCoverageFiles(result.Coverage); err != nil {
		return err
	}
	directive, owner := cfg.resolveDirective(result.TestName)
	if result.Disabled != directive.Disabled || result.Quarantined != directive.Quarantined ||
		result.AttemptToFix != directive.AttemptToFix || result.AttemptToFixOwn != (owner == result.TestName) {
		return fmt.Errorf("%w: subtree directive mismatch", errProcessRetryResultInvalid)
	}
	if result.SkippedByITR && (result.ITRForcedRun || result.Status != processRetryStatusSkip || result.SkipReason != constants.SkippedByITRReason) {
		return fmt.Errorf("%w: invalid subtree ITR mirrors", errProcessRetryResultInvalid)
	}
	switch result.Status {
	case processRetryStatusPass, processRetryStatusSkip:
	case processRetryStatusFail:
		if result.Skipped {
			return fmt.Errorf("%w: invalid subtree fail mirrors", errProcessRetryResultInvalid)
		}
	default:
		return fmt.Errorf("%w: invalid subtree status", errProcessRetryResultInvalid)
	}
	return validateProcessRetryResultStatus(processRetryResultFromSubtree(result))
}

func validateProcessRetryTestSource(source *processRetryTestSource) error {
	if source == nil {
		return nil
	}
	if source.RuntimePath == "" || source.RuntimeStartLine <= 0 || source.RuntimeEndLine < 0 ||
		source.RuntimeEndLine > 0 && source.RuntimeEndLine < source.RuntimeStartLine ||
		!processRetryJSONStringFits(source.RuntimePath, processRetryErrorMessageMaxBytes) {
		return fmt.Errorf("%w: invalid subtree source", errProcessRetryResultInvalid)
	}
	return nil
}

func validateProcessRetryCoverageFiles(files []coverage.ProcessTestCoverageFile) error {
	if len(files) > processRetrySubtreeMaxResults {
		return fmt.Errorf("%w: too many process coverage files", errProcessRetryResultInvalid)
	}
	for _, file := range files {
		if strings.TrimSpace(file.Name) == "" || !processRetryJSONStringFits(file.Name, processRetryErrorMessageMaxBytes) {
			return fmt.Errorf("%w: invalid process coverage file", errProcessRetryResultInvalid)
		}
	}
	return nil
}

type quarantinedRaceInvocation struct {
	cfg          *processRetrySubtreeConfig
	attempt      processRetryAttemptResult
	attemptIndex int

	finalBecauseOfFailfast bool
}

// runQuarantinedRaceProcessIsolation replaces only the selected quarantined
// root. For TestCheckout/card, the parent may still run TestCheckout/paypal,
// while one child runs card and every descendant beneath it. The parent owns
// retries and CI Visibility events; the child owns no session or transport.
func runQuarantinedRaceProcessIsolation(
	t *testing.T,
	testInfo *commonInfo,
	parentExecMeta *testExecutionMetadata,
	featureMeta *additionalFeatureMetadata,
	processCtx *quarantinedRaceProcessContext,
) {
	t.Helper()
	execMeta := getTestMetadata(t)
	createdMetadata := false
	if execMeta == nil {
		execMeta = createTestMetadata(t, nil)
		createdMetadata = true
	}
	if createdMetadata {
		defer deleteTestMetadata(t)
	}
	applyAdditionalFeatureMetadataToExecution(execMeta, featureMeta)
	propagateTestExecutionMetadataFlags(execMeta, parentExecMeta)
	execMeta.quarantinedRaceProcess = processCtx
	execMeta.hasAdditionalFeatureWrapper = true

	cfg, err := buildProcessRetrySubtreeConfig(processCtx, testInfo, execMeta, parentExecMeta)
	if err != nil {
		failQuarantinedRaceIsolation(t, testInfo, execMeta, processRetryAttemptResult{SetupFailure: true, Err: err, ExitCode: processRetryExitCodeUnset, StartTime: time.Now(), FinishTime: time.Now()})
		return
	}
	lease, err := acquireProcessRetryGroupLease()
	if err != nil {
		failQuarantinedRaceIsolation(t, testInfo, execMeta, processRetryAttemptResult{SetupFailure: true, Err: err, ExitCode: processRetryExitCodeUnset, StartTime: time.Now(), FinishTime: time.Now()})
		return
	}
	defer lease.release()
	var parallelGroup *retryAttemptGroup
	var parallelReason string
	var parallelOnce sync.Once
	parallelBridge := func() error {
		// Serial roots never pay for or depend on the private scheduler bridge.
		parallelOnce.Do(func() {
			parallelGroup, parallelReason = newRetryAttemptGroupWithOutputObservation(t, false)
		})
		if parallelReason != "" {
			return fmt.Errorf("quarantined race parallel admission: %s", parallelReason)
		}
		parallelGroup.transitionOriginalToParallel()
		return nil
	}
	defer func() {
		if parallelGroup != nil {
			parallelGroup.retire()
		}
	}()

	rootTotal := 1
	if cfg.OwnsAttemptToFix {
		rootTotal = processRetryAttemptToFixExecutionCount(cfg.AttemptToFixRetries)
	}
	invocations := make([]quarantinedRaceInvocation, 0, rootTotal)

	for idx := 0; idx < rootTotal; idx++ {
		attempt := runQuarantinedRaceInvocation(t, processCtx, lease, cfg, idx, parallelBridge)
		if processRetryInfrastructureFailure(attempt) {
			failQuarantinedRaceIsolation(t, testInfo, execMeta, attempt)
			return
		}
		invocations = append(invocations, quarantinedRaceInvocation{
			cfg: cfg, attempt: attempt, attemptIndex: idx,
		})
		if quarantinedRaceFailfastStopsContinuation(attempt) {
			invocations[len(invocations)-1].finalBecauseOfFailfast = true
			break
		}
		if cfg.AttemptToFixRetries <= 0 {
			continue
		}
		stop, failed := continueQuarantinedRaceDescendantFamilies(
			t, testInfo, execMeta, processCtx, lease, cfg, attempt, parallelBridge, &invocations,
		)
		if failed {
			return
		}
		if stop {
			break
		}
	}

	replayQuarantinedRaceInvocations(testInfo, invocations)
	// A valid quarantined failure, panic, or race remains visible in CI
	// Visibility but is intentionally masked from the package result. An
	// infrastructure error returns above through Fail and is never quarantined.
	t.SkipNow()
}

// continueQuarantinedRaceDescendantFamilies completes each independently owned
// Attempt-to-Fix family before continuing its ancestor. This depth-first order
// keeps a deeper owner's retry adjacent to the initial execution from the same
// enclosing invocation, so replay cannot associate it with a later family.
func continueQuarantinedRaceDescendantFamilies(
	t *testing.T,
	testInfo *commonInfo,
	execMeta *testExecutionMetadata,
	processCtx *quarantinedRaceProcessContext,
	lease *processRetryGroupLease,
	cfg *processRetrySubtreeConfig,
	attempt processRetryAttemptResult,
	parallelBridge func() error,
	invocations *[]quarantinedRaceInvocation,
) (stop, failed bool) {
	for _, result := range directQuarantinedRaceAttemptOwners(attempt.Result.Subtests, cfg.SelectedRoot) {
		continuationCfg, err := cfg.forSelectedRoot(result.TestName)
		if err != nil {
			failQuarantinedRaceIsolation(t, testInfo, execMeta, processRetryAttemptResult{SetupFailure: true, Err: err, ExitCode: processRetryExitCodeUnset, StartTime: time.Now(), FinishTime: time.Now()})
			return false, true
		}
		if stop, failed := continueQuarantinedRaceDescendantFamilies(
			t, testInfo, execMeta, processCtx, lease, continuationCfg, attempt, parallelBridge, invocations,
		); stop || failed {
			return stop, failed
		}
		for idx := 1; idx < processRetryAttemptToFixExecutionCount(cfg.AttemptToFixRetries); idx++ {
			next := runQuarantinedRaceInvocation(t, processCtx, lease, continuationCfg, idx, parallelBridge)
			if processRetryInfrastructureFailure(next) {
				failQuarantinedRaceIsolation(t, &commonInfo{
					moduleName: testInfo.moduleName,
					suiteName:  testInfo.suiteName,
					testName:   result.TestName,
					identity:   newTestIdentity(testInfo.moduleName, testInfo.suiteName, result.TestName),
				}, execMeta, next)
				return false, true
			}
			*invocations = append(*invocations, quarantinedRaceInvocation{
				cfg: continuationCfg, attempt: next, attemptIndex: idx,
			})
			if quarantinedRaceFailfastStopsContinuation(next) {
				(*invocations)[len(*invocations)-1].finalBecauseOfFailfast = true
				return true, false
			}
			if stop, failed := continueQuarantinedRaceDescendantFamilies(
				t, testInfo, execMeta, processCtx, lease, continuationCfg, next, parallelBridge, invocations,
			); stop || failed {
				return stop, failed
			}
		}
	}
	return false, false
}

func directQuarantinedRaceAttemptOwners(results []processRetrySubtreeResult, root string) []processRetrySubtreeResult {
	ownerNames := make(map[string]struct{}, len(results))
	for _, result := range results {
		if result.AttemptToFixOwn && result.TestName != root && processRetryNameWithinRoot(result.TestName, root) {
			ownerNames[result.TestName] = struct{}{}
		}
	}
	owners := make([]processRetrySubtreeResult, 0, len(ownerNames))
	for _, result := range results {
		if _, ok := ownerNames[result.TestName]; !ok {
			continue
		}
		direct := true
		for ancestor := result.TestName; len(ancestor) > len(root); {
			ancestor = ancestor[:strings.LastIndexByte(ancestor, '/')]
			if _, ok := ownerNames[ancestor]; ok {
				direct = false
				break
			}
		}
		if direct {
			owners = append(owners, result)
		}
	}
	return owners
}

// AttemptToFixRetries is the configured total execution count in the existing
// in-process retry path, despite its historical name.
func processRetryAttemptToFixExecutionCount(configured int) int {
	return max(configured, 1)
}

func quarantinedRaceFailfastStopsContinuation(attempt processRetryAttemptResult) bool {
	return retryAttemptFailfastEnabled() && effectiveProcessRetryStatus(attempt, false).Failed
}

func runQuarantinedRaceInvocation(
	t *testing.T,
	processCtx *quarantinedRaceProcessContext,
	lease *processRetryGroupLease,
	cfg *processRetrySubtreeConfig,
	attemptIndex int,
	parallelBridge func() error,
) processRetryAttemptResult {
	deadline, deadlineOK := t.Deadline()
	baseline := captureProcessRetryLaunchBaselineFromTemplate(processCtx.launchTemplate)
	// The parent already admitted this root. Replacing only this invocation's
	// selector lets the child re-enter required ancestors without executing a
	// sibling that happened to match the user's broader -run expression.
	baseline.argsSnapshot.runSelector = processRetryExactRunPattern(cfg.SelectedRoot)
	attempt := runProcessRetryAttemptWithBaselineAndShutdown(
		context.Background(),
		processRetryChildConfig{
			TestName:          cfg.SelectedRoot,
			Attempt:           attemptIndex + 1,
			RetryReason:       processRetrySubtreeReason,
			MRunEpoch:         processCtx.mRunEpoch,
			InvocationOrdinal: processCtx.invocations.Add(1),
			Subtree:           cfg,
		},
		deadline,
		deadlineOK,
		baseline,
		lease.shutdown,
		parallelBridge,
	)
	defer func() {
		if attempt.Cleanup != nil {
			attempt.Cleanup()
		}
	}()
	if cfg.CollectAggregate && attempt.Result.Status != "" && attempt.Result.Status != processRetryStatusNotRun {
		if err := coverage.MergeProcessCoverageProfile(processRetrySubtreeCoveragePath(filepath.Join(attempt.TempDir, "result.json"))); err != nil {
			attempt.Err = errors.Join(attempt.Err, fmt.Errorf("aggregate process coverage: %w", err))
		}
	}
	return attempt
}

func processRetryInfrastructureFailure(attempt processRetryAttemptResult) bool {
	effective := effectiveProcessRetryStatus(attempt, false)
	if !effective.Failed {
		return false
	}
	switch effective.FailureKind {
	case "test_fail", "test_panic", "test_race":
		return false
	default:
		return true
	}
}

func failQuarantinedRaceIsolation(t *testing.T, testInfo *commonInfo, execMeta *testExecutionMetadata, attempt processRetryAttemptResult) {
	if attempt.StartTime.IsZero() {
		attempt.StartTime = time.Now()
	}
	if attempt.FinishTime.IsZero() {
		attempt.FinishTime = attempt.StartTime
	}
	execMeta.retryContinuationDecided = true
	execMeta.retryContinuationAdmitted = false
	effective := effectiveProcessRetryStatus(attempt, false)
	t.Logf("CI Visibility quarantined test isolation failed: %s: %v", effective.FailureKind, attempt.Err)
	if attempt.OutputTail != "" {
		t.Log(attempt.OutputTail)
	}
	if len(testInfo.identity.Segments) > 1 {
		addModulesCounters(testInfo.moduleName, 1)
		addSuitesCounters(testInfo.suiteName, 1)
	}
	finishProcessRetryTestEvent(testInfo, execMeta, attempt, nil, nil)
	module := session.GetOrCreateModule(testInfo.moduleName)
	suite := module.GetOrCreateSuite(testInfo.suiteName)
	checkModuleAndSuite(module, suite)
	t.Fail()
}

func (cfg *processRetrySubtreeConfig) forSelectedRoot(name string) (*processRetrySubtreeConfig, error) {
	directive, _ := cfg.resolveDirective(name)
	next := &processRetrySubtreeConfig{
		Version:              processRetrySubtreeVersion,
		SelectedRoot:         name,
		Root:                 directive,
		AttemptToFixRetries:  cfg.AttemptToFixRetries,
		OwnsAttemptToFix:     directive.AttemptToFix,
		CollectPerTest:       cfg.CollectPerTest,
		CollectAggregate:     cfg.CollectAggregate,
		ITRCoverageActive:    cfg.ITRCoverageActive,
		ImpactedTestsEnabled: cfg.ImpactedTestsEnabled,
	}
	for _, candidate := range cfg.Directives {
		if candidate.TestName != name && processRetryNameWithinRoot(candidate.TestName, name) {
			next.Directives = append(next.Directives, candidate)
		}
	}
	for _, candidate := range cfg.ITR {
		if processRetryNameWithinRoot(candidate.TestName, name) {
			next.ITR = append(next.ITR, candidate)
		}
	}
	return next, validateProcessRetrySubtreeConfig(next, name)
}

func replayQuarantinedRaceInvocations(testInfo *commonInfo, invocations []quarantinedRaceInvocation) {
	if testInfo == nil {
		return
	}
	outcomes := make(map[string]retryOutcomeAccumulator)
	counted := make(map[string]struct{})
	for _, invocation := range invocations {
		for _, result := range invocation.attempt.Result.Subtests {
			replayQuarantinedRaceEvent(testInfo, invocation, result, outcomes, counted)
		}
		root := processRetrySubtreeRootFromInvocation(invocation)
		replayQuarantinedRaceEvent(testInfo, invocation, root, outcomes, counted)
	}
	module := session.GetOrCreateModule(testInfo.moduleName)
	suite := module.GetOrCreateSuite(testInfo.suiteName)
	for range counted {
		checkModuleAndSuite(module, suite)
	}
}

func processRetrySubtreeRootFromInvocation(invocation quarantinedRaceInvocation) processRetrySubtreeResult {
	root := processRetrySubtreeResultFromEnvelope(invocation.attempt.Result, invocation.cfg)
	if root.Failed && invocation.attempt.OutputTail != "" {
		// Direct child stdout and stderr are not present in testing's buffered
		// subtree output. A failed selected root owns the complete process tail.
		root.OutputTail = invocation.attempt.OutputTail
		root.OutputTruncated = invocation.attempt.OutputTruncated
	}
	return root
}

func replayQuarantinedRaceEvent(
	testInfo *commonInfo,
	invocation quarantinedRaceInvocation,
	result processRetrySubtreeResult,
	outcomes map[string]retryOutcomeAccumulator,
	counted map[string]struct{},
) {
	identity := newTestIdentity(testInfo.moduleName, testInfo.suiteName, result.TestName)
	if _, ok := counted[result.TestName]; !ok {
		// The parent already counted a top-level test before entering its
		// wrapper. Subtests bypass the normal runSubtest path, so only they
		// need a matching increment; every unique event still needs one
		// matching checkModuleAndSuite call after replay.
		if len(identity.Segments) > 1 {
			addModulesCounters(testInfo.moduleName, 1)
			addSuitesCounters(testInfo.suiteName, 1)
		}
		counted[result.TestName] = struct{}{}
	}
	_, attemptOwner := invocation.cfg.resolveDirective(result.TestName)
	ownsAttemptToFix := attemptOwner == result.TestName
	prior := outcomes[result.TestName]
	attemptIndex := invocation.attemptIndex
	if attemptOwner != "" && attemptOwner != invocation.cfg.SelectedRoot {
		// A descendant that cleared an inherited directive and started its own
		// family is on its first execution in this enclosing invocation.
		prior = retryOutcomeAccumulator{}
		attemptIndex = 0
	}
	execMeta := &testExecutionMetadata{
		identity:                      identity,
		isQuarantined:                 result.Quarantined,
		isDisabled:                    result.Disabled,
		isAttemptToFix:                result.AttemptToFix,
		isItrForcedRun:                result.ITRForcedRun,
		isItrSkipped:                  result.SkippedByITR,
		isAModifiedTest:               result.Modified,
		hasAdditionalFeatureWrapper:   true,
		hasExplicitQuarantined:        true,
		hasExplicitDisabled:           true,
		hasExplicitAttemptToFix:       true,
		suppressParentRetryMetadata:   true,
		shouldOrchestrateAttemptToFix: ownsAttemptToFix,
	}
	if attemptOwner != "" {
		attemptTotal := processRetryAttemptToFixExecutionCount(invocation.cfg.AttemptToFixRetries)
		execMeta.isARetry = attemptIndex > 0
		execMeta.isLastRetry = invocation.finalBecauseOfFailfast || attemptIndex == attemptTotal-1
		execMeta.remainingRetries = int64(attemptTotal - attemptIndex)
		execMeta.initialRetryCount = int64(invocation.cfg.AttemptToFixRetries)
		execMeta.initialRetryCountSet = true
		execMeta.retryContinuationDecided = true
		execMeta.retryContinuationAdmitted = !execMeta.isLastRetry
		execMeta.anyExecutionPassed = prior.anyPassed()
		execMeta.anyExecutionFailed = prior.anyFailed()
		execMeta.allAttemptsPassed = prior.allAttemptsPassed()
		execMeta.allRetriesFailed = execMeta.isARetry && prior.allRetriesFailed()
	} else {
		execMeta.retryContinuationDecided = true
		execMeta.anyExecutionPassed = result.Status == processRetryStatusPass
		execMeta.anyExecutionFailed = result.Failed
		execMeta.allAttemptsPassed = result.Status == processRetryStatusPass
	}
	attempt := processRetryAttemptFromSubtreeResult(result)
	finishProcessRetryTestEvent(&commonInfo{
		moduleName: testInfo.moduleName,
		suiteName:  testInfo.suiteName,
		testName:   result.TestName,
		identity:   identity,
	}, execMeta, attempt, nil, nil)
	if attemptOwner != "" {
		prior.observe(result.Failed, result.Skipped)
		outcomes[result.TestName] = prior
	}
}

func processRetrySubtreeResultFromEnvelope(result processRetryResult, cfg *processRetrySubtreeConfig) processRetrySubtreeResult {
	directive, owner := cfg.resolveDirective(result.TestName)
	return processRetrySubtreeResult{
		TestName:        result.TestName,
		Status:          normalizedProcessRetrySubtreeStatus(result.Status),
		StartUnixNano:   result.StartUnixNano,
		FinishUnixNano:  result.FinishUnixNano,
		DurationNanos:   result.DurationNanos,
		Failed:          result.Failed,
		Skipped:         result.Skipped,
		Panic:           result.Panic,
		RaceDetected:    result.RaceDetected,
		Disabled:        directive.Disabled,
		Quarantined:     directive.Quarantined,
		AttemptToFix:    directive.AttemptToFix,
		AttemptToFixOwn: owner == result.TestName,
		SkippedByITR:    result.SkippedByITR,
		ITRForcedRun:    result.ITRForcedRun,
		Modified:        result.Modified,
		ErrorType:       result.ErrorType,
		ErrorMessage:    result.ErrorMessage,
		ErrorStack:      result.ErrorStack,
		SkipReason:      result.SkipReason,
		OutputTail:      result.OutputTail,
		OutputTruncated: result.OutputTruncated,
		Source:          result.Source,
		Coverage:        result.Coverage,
	}
}

func normalizedProcessRetrySubtreeStatus(status processRetryStatus) processRetryStatus {
	if isProcessRetryControlledTerminalStatus(status) {
		return processRetryStatusFail
	}
	return status
}

func processRetryAttemptFromSubtreeResult(result processRetrySubtreeResult) processRetryAttemptResult {
	converted := processRetryResultFromSubtree(result)
	outputTail := result.OutputTail
	if result.OutputTruncated {
		outputTail = processRetryOutputTruncationMarker + outputTail
	}
	attempt := processRetryAttemptResult{
		Result:             converted,
		OutputTail:         outputTail,
		OutputTruncated:    result.OutputTruncated,
		ExitStatusObserved: true,
		ExitCode:           0,
		StartTime:          time.Unix(0, result.StartUnixNano),
		FinishTime:         time.Unix(0, result.FinishUnixNano),
	}
	if result.Failed {
		attempt.ExitCode = processRetryFailureExitCode
	}
	if result.Panic {
		attempt.ExitCode = processRetryControlledPanicExitCode
		attempt.ControlledTerminalCommitted = true
	}
	return attempt
}

func processRetryResultFromSubtree(result processRetrySubtreeResult) processRetryResult {
	return processRetryResult{
		Version:         1,
		TestName:        result.TestName,
		Status:          result.Status,
		StartUnixNano:   result.StartUnixNano,
		FinishUnixNano:  result.FinishUnixNano,
		DurationNanos:   result.DurationNanos,
		Failed:          result.Failed,
		Skipped:         result.Skipped,
		Panic:           result.Panic,
		RaceDetected:    result.RaceDetected,
		ErrorType:       result.ErrorType,
		ErrorMessage:    result.ErrorMessage,
		ErrorStack:      result.ErrorStack,
		SkipReason:      result.SkipReason,
		OutputTail:      result.OutputTail,
		OutputTruncated: result.OutputTruncated,
		Source:          result.Source,
		Coverage:        result.Coverage,
	}
}
