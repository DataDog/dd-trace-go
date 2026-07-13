// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

type retryExecutionMode int

const (
	retryExecutionModeInProcess retryExecutionMode = iota
	retryExecutionModeProcess
)

func retryExecutionModeFromEnv() retryExecutionMode {
	raw := strings.ToLower(strings.TrimSpace(env.Get(constants.CIVisibilityRetryExecutionModeEnvironmentVariable)))
	switch raw {
	case "", "in_process":
		return retryExecutionModeInProcess
	case "process":
		return retryExecutionModeProcess
	default:
		log.Debug("civisibility: unsupported retry execution mode, using in_process")
		return retryExecutionModeInProcess
	}
}

func processRetryMaxConcurrencyFromEnv(defaultValue int) int {
	if defaultValue < 1 {
		defaultValue = 1
	}
	raw := strings.TrimSpace(env.Get(constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable))
	if raw == "" {
		return defaultValue
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		log.Debug("civisibility: unsupported retry process max concurrency, using %d", defaultValue)
		return defaultValue
	}
	return n
}

func processRetryTimeoutFromEnv() (time.Duration, bool) {
	raw := strings.TrimSpace(env.Get(constants.CIVisibilityRetryProcessTimeoutEnvironmentVariable))
	if raw == "" {
		return 0, false
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Debug("civisibility: unsupported retry process timeout, ignoring")
		return 0, false
	}
	return d, true
}

type processRetryChildConfig struct {
	ResultPath  string
	TestName    string
	Attempt     int
	RetryReason string
}

type processRetryStatus string

const (
	processRetryStatusPass   processRetryStatus = "pass"
	processRetryStatusFail   processRetryStatus = "fail"
	processRetryStatusSkip   processRetryStatus = "skip"
	processRetryStatusNotRun processRetryStatus = "not_run"

	processRetryErrorTypeMaxBytes          = 256
	processRetryErrorMessageMaxBytes       = 8 * 1024
	processRetryErrorStackMaxBytes         = 32 * 1024
	processRetrySkipReasonMaxBytes         = 8 * 1024
	processRetryResultErrorMaxBytes        = 256
	processRetryResultMaxBytes             = 64 * 1024
	processRetryTruncationMarker           = "[dd-trace-go: process retry panic data truncated]"
	processRetryMetadataTruncationMarker   = "[dd-trace-go: process retry metadata truncated]"
	processRetryOutputTruncationMarker     = "\n[dd-trace-go: process retry output truncated]\n"
	processRetryOutputMaxBytes             = 32 * 1024
	processRetryStreamMaxBytes             = 32 * 1024
	processRetryExitCodeUnset              = -1
	processRetryOutputDrainWait            = 1 * time.Second
	processRetryOutputDrainBudget          = processRetryOutputDrainWait
	processRetryKillGracePeriod            = 2 * time.Second
	processRetryPostKillWait               = 2 * time.Second
	processRetryParentDeadlineSafetyMargin = 500 * time.Millisecond
	processRetryShutdownWait               = processRetryKillGracePeriod + processRetryPostKillWait + processRetryOutputDrainBudget + processRetryParentDeadlineSafetyMargin
	processRetryDefaultTimeout             = 10 * time.Minute
)

type processRetryResult struct {
	Version        int                `json:"version"`
	TestName       string             `json:"test_name"`
	Attempt        int                `json:"attempt"`
	RetryReason    string             `json:"retry_reason"`
	Status         processRetryStatus `json:"status"`
	StartUnixNano  int64              `json:"start_unix_nano"`
	FinishUnixNano int64              `json:"finish_unix_nano"`
	DurationNanos  int64              `json:"duration_nanos"`
	Failed         bool               `json:"failed"`
	Skipped        bool               `json:"skipped"`
	Panic          bool               `json:"panic"`
	ErrorType      string             `json:"error_type,omitempty"`
	ErrorMessage   string             `json:"error_message,omitempty"`
	ErrorStack     string             `json:"error_stack,omitempty"`
	SkipReason     string             `json:"skip_reason,omitempty"`
	ResultError    string             `json:"result_error,omitempty"`
}

type processRetryErrorInfo struct {
	Type    string
	Message string
	Stack   string
}

var (
	errProcessRetryMissingResultPath   = errors.New("missing_result_path")
	errProcessRetryMissingTestName     = errors.New("missing_test_name")
	errProcessRetryMissingAttempt      = errors.New("missing_attempt")
	errProcessRetryInvalidAttempt      = errors.New("invalid_attempt")
	errProcessRetryMissingRetryReason  = errors.New("missing_retry_reason")
	errProcessRetryResultMissing       = errors.New("process retry result missing")
	errProcessRetryResultInvalid       = errors.New("process retry result invalid")
	errProcessRetryProcessNotStarted   = errors.New("process retry child process not started")
	errProcessRetryTreeUnsupported     = errors.New("process retry process-tree containment unsupported")
	errProcessRetryChildUnreaped       = errors.New("process retry child process did not exit after kill")
	errProcessRetryLaunchDisabled      = errors.New("process retry launches disabled after unreaped child")
	errProcessRetryLaunchCanceled      = errors.New("process retry launch canceled before child start")
	errProcessRetryLaunchDeadline      = errors.New("process retry parent deadline exhausted before child start")
	errProcessRetryShutdown            = errors.New("process retry shutdown started")
	errProcessRetryOutputDrainTimedOut = errors.New("process retry output drain timed out")
	errProcessRetryContainmentLost     = errors.New("process retry process-tree containment lost")
)

var lookupProcessRetryChildTransport = integrations.LookupProcessRetryChildTransport

func isProcessRetryChild() bool {
	value, ok := lookupProcessRetryChildTransport(constants.CIVisibilityInternalRetryProcessChild)
	if !ok {
		return false
	}
	enabled, err := strconv.ParseBool(value)
	return err == nil && enabled
}

func processRetryChildConfigFromEnv() (processRetryChildConfig, error) {
	resultPath, ok := lookupProcessRetryChildTransport(constants.CIVisibilityInternalRetryProcessResultPath)
	if !ok || strings.TrimSpace(resultPath) == "" {
		return processRetryChildConfig{}, errProcessRetryMissingResultPath
	}
	testName, ok := lookupProcessRetryChildTransport(constants.CIVisibilityInternalRetryProcessTestName)
	if !ok || strings.TrimSpace(testName) == "" {
		return processRetryChildConfig{}, errProcessRetryMissingTestName
	}
	attemptRaw, ok := lookupProcessRetryChildTransport(constants.CIVisibilityInternalRetryProcessAttempt)
	if !ok || strings.TrimSpace(attemptRaw) == "" {
		return processRetryChildConfig{}, errProcessRetryMissingAttempt
	}
	attempt, err := strconv.Atoi(strings.TrimSpace(attemptRaw))
	if err != nil || attempt < 1 {
		return processRetryChildConfig{}, errProcessRetryInvalidAttempt
	}
	reason, ok := lookupProcessRetryChildTransport(constants.CIVisibilityInternalRetryProcessReason)
	if !ok || strings.TrimSpace(reason) == "" {
		return processRetryChildConfig{}, errProcessRetryMissingRetryReason
	}
	return processRetryChildConfig{
		ResultPath:  resultPath,
		TestName:    testName,
		Attempt:     attempt,
		RetryReason: reason,
	}, nil
}

func bootstrapProcessRetryChild() (processRetryChildConfig, error) {
	cfg, err := processRetryChildConfigFromEnv()
	if err != nil {
		return processRetryChildConfig{}, err
	}
	if integrations.ProcessRetryChildTransportError() != nil {
		return cfg, errors.New("retry child transport cleanup failed")
	}
	return cfg, nil
}

func processRetryChildConfigErrorReason(err error) string {
	switch {
	case errors.Is(err, errProcessRetryMissingResultPath):
		return "missing_result_path"
	case errors.Is(err, errProcessRetryMissingTestName):
		return "missing_test_name"
	case errors.Is(err, errProcessRetryMissingAttempt):
		return "missing_attempt"
	case errors.Is(err, errProcessRetryInvalidAttempt):
		return "invalid_attempt"
	case errors.Is(err, errProcessRetryMissingRetryReason):
		return "missing_retry_reason"
	default:
		return "invalid_child_config"
	}
}

func processRetryCoverageActive(perTestCoverageEnabled bool) bool {
	if perTestCoverageEnabled {
		return true
	}
	if testing.CoverMode() != "" {
		return true
	}
	for _, name := range []string{"test.coverprofile", "test.gocoverdir"} {
		if f := flag.Lookup(name); f != nil && strings.TrimSpace(f.Value.String()) != "" {
			return true
		}
	}
	return false
}

func processRetryFuzzActive() bool {
	for _, name := range []string{"test.fuzz", "fuzz", "test.fuzzcachedir"} {
		if f := flag.Lookup(name); f != nil && strings.TrimSpace(f.Value.String()) != "" {
			return true
		}
	}
	if f := flag.Lookup("test.fuzzworker"); f != nil && f.Value.String() == "true" {
		return true
	}
	active := false
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "test.fuzztime", "test.fuzzminimizetime":
			active = true
		}
	})
	return active
}

type processRetrySupportHooks struct {
	childCleanupSupported      func() bool
	testingMWorkloadsSupported func() bool
}

var processRetrySupportHooksOverride atomic.Pointer[processRetrySupportHooks]

func currentProcessRetrySupportHooks() processRetrySupportHooks {
	if hooks := processRetrySupportHooksOverride.Load(); hooks != nil {
		resolved := *hooks
		if resolved.childCleanupSupported == nil {
			resolved.childCleanupSupported = processRetryChildCleanupSupportedDefault
		}
		if resolved.testingMWorkloadsSupported == nil {
			resolved.testingMWorkloadsSupported = processRetryTestingMWorkloadsSupportedDefault
		}
		return resolved
	}
	return processRetrySupportHooks{
		childCleanupSupported:      processRetryChildCleanupSupportedDefault,
		testingMWorkloadsSupported: processRetryTestingMWorkloadsSupportedDefault,
	}
}

func setProcessRetrySupportHooksForTesting(t testing.TB, hooks processRetrySupportHooks) func() {
	t.Helper()
	old := processRetrySupportHooksOverride.Swap(&hooks)
	var once sync.Once
	restore := func() {
		once.Do(func() {
			processRetrySupportHooksOverride.Store(old)
		})
	}
	return restore
}

func processRetryChildCleanupSupported() bool {
	return currentProcessRetrySupportHooks().childCleanupSupported()
}

func processRetryChildCleanupSupportedDefault() bool {
	return processRetryChildCleanupLayoutSupported(getTestingInternalsLayout())
}

func processRetryChildCleanupLayoutSupported(layout *testingInternalsLayout) bool {
	return layout != nil && !layout.disabled && allAvailable(
		layout.common.mu, layout.common.sub, layout.common.barrier, layout.common.signal,
		layout.common.isParallel, layout.common.finished, layout.tstate.unsafeField,
	)
}

func processRetryTestingMWorkloadsSupported() bool {
	return currentProcessRetrySupportHooks().testingMWorkloadsSupported()
}

func processRetryTestingMWorkloadsSupportedDefault() bool {
	m := &testing.M{}
	return getInternalTestArray(m) != nil &&
		getInternalBenchmarkArray(m) != nil &&
		getInternalFuzzTargetArray(m) != nil &&
		getInternalExampleArray(m) != nil
}

func buildProcessRetryEnv(base []string, cfg processRetryChildConfig) []string {
	result := make([]string, 0, len(base)+5)
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			result = append(result, entry)
			continue
		}
		if isProcessRetryInternalEnvKey(key) {
			continue
		}
		result = append(result, entry)
	}
	result = append(result,
		constants.CIVisibilityInternalRetryProcessChild+"=true",
		constants.CIVisibilityInternalRetryProcessResultPath+"="+cfg.ResultPath,
		constants.CIVisibilityInternalRetryProcessTestName+"="+cfg.TestName,
		constants.CIVisibilityInternalRetryProcessAttempt+"="+strconv.Itoa(cfg.Attempt),
		constants.CIVisibilityInternalRetryProcessReason+"="+cfg.RetryReason,
	)
	return result
}

func isProcessRetryInternalEnvKey(key string) bool {
	return integrations.IsProcessRetryChildTransportKey(key)
}

type processRetryFlagArity int

const (
	processRetryFlagBool processRetryFlagArity = iota
	processRetryFlagValue
)

var processRetryStripFlags = map[string]processRetryFlagArity{
	"-test.run":              processRetryFlagValue,
	"-run":                   processRetryFlagValue,
	"-test.count":            processRetryFlagValue,
	"-count":                 processRetryFlagValue,
	"-test.bench":            processRetryFlagValue,
	"-bench":                 processRetryFlagValue,
	"-test.list":             processRetryFlagValue,
	"-list":                  processRetryFlagValue,
	"-test.fuzz":             processRetryFlagValue,
	"-fuzz":                  processRetryFlagValue,
	"-test.skip":             processRetryFlagValue,
	"-skip":                  processRetryFlagValue,
	"-test.cpu":              processRetryFlagValue,
	"-cpu":                   processRetryFlagValue,
	"-test.timeout":          processRetryFlagValue,
	"-timeout":               processRetryFlagValue,
	"-test.testlogfile":      processRetryFlagValue,
	"-test.gocoverdir":       processRetryFlagValue,
	"-test.coverprofile":     processRetryFlagValue,
	"-test.fuzzcachedir":     processRetryFlagValue,
	"-test.fuzzworker":       processRetryFlagBool,
	"-test.fuzztime":         processRetryFlagValue,
	"-test.fuzzminimizetime": processRetryFlagValue,
	"-test.outputdir":        processRetryFlagValue,
	"-test.cpuprofile":       processRetryFlagValue,
	"-test.memprofile":       processRetryFlagValue,
	"-test.blockprofile":     processRetryFlagValue,
	"-test.mutexprofile":     processRetryFlagValue,
	"-test.trace":            processRetryFlagValue,
	"-test.artifacts":        processRetryFlagBool,
}

type processRetryBoolFlag interface {
	IsBoolFlag() bool
}

type processRetryBoundedOutput struct {
	mu        locking.Mutex
	maxBytes  int64
	total     int64
	tail      []byte
	truncated bool
}

func newProcessRetryBoundedOutput(maxBytes int64) *processRetryBoundedOutput {
	if maxBytes < 0 {
		maxBytes = 0
	}
	return &processRetryBoundedOutput{
		maxBytes: maxBytes,
		tail:     make([]byte, 0, int(maxBytes)),
	}
}

func (w *processRetryBoundedOutput) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.total += int64(len(p))
	w.appendTailLocked(p)
	return len(p), nil
}

func (w *processRetryBoundedOutput) appendTailLocked(p []byte) {
	if w.maxBytes <= 0 {
		if len(p) > 0 {
			w.truncated = true
		}
		return
	}
	if int64(len(p)) >= w.maxBytes {
		w.tail = append(w.tail[:0], p[len(p)-int(w.maxBytes):]...)
		w.truncated = true
		return
	}
	w.tail = append(w.tail, p...)
	if int64(len(w.tail)) > w.maxBytes {
		drop := len(w.tail) - int(w.maxBytes)
		copy(w.tail, w.tail[drop:])
		w.tail = w.tail[:int(w.maxBytes)]
		w.truncated = true
	}
}

func (w *processRetryBoundedOutput) Tail() (string, bool) {
	if w == nil {
		return "", false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.tail), w.truncated || w.total > int64(len(w.tail))
}

type processRetryOutputCapture struct {
	mu          locking.Mutex
	sink        *processRetryBoundedOutput
	readPipe    *os.File
	writePipe   *os.File
	copyDone    chan struct{}
	copyStarted bool
	finished    bool
	copyErr     error
	aborted     bool
}

func newProcessRetryOutputCapture(maxBytes int64) (*processRetryOutputCapture, error) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	return &processRetryOutputCapture{
		sink:      newProcessRetryBoundedOutput(maxBytes),
		readPipe:  readPipe,
		writePipe: writePipe,
		copyDone:  make(chan struct{}),
	}, nil
}

func (c *processRetryOutputCapture) ChildWriter() *os.File {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writePipe
}

func (c *processRetryOutputCapture) StartCopy() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.copyStarted || c.finished {
		c.mu.Unlock()
		return
	}
	c.copyStarted = true
	sink := c.sink
	readPipe := c.readPipe
	c.mu.Unlock()
	go func() {
		_, copyErr := io.Copy(sink, readPipe)
		c.complete(errors.Join(
			ignoreProcessRetryClosedError(copyErr),
			ignoreProcessRetryClosedError(readPipe.Close()),
		))
	}()
}

func ignoreProcessRetryClosedError(err error) error {
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

func (c *processRetryOutputCapture) complete(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	c.finished = true
	c.copyErr = err
	c.readPipe = nil
	close(c.copyDone)
}

func (c *processRetryOutputCapture) completedError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.copyErr
}

func (c *processRetryOutputCapture) CloseParentWriter() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writePipe == nil {
		return nil
	}
	err := c.writePipe.Close()
	c.writePipe = nil
	return err
}

func (c *processRetryOutputCapture) FinishAfterWait(timeout time.Duration) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.finished {
		err := c.copyErr
		c.mu.Unlock()
		return err
	}
	if !c.copyStarted {
		c.mu.Unlock()
		return c.CloseSetupFailure()
	}
	copyDone := c.copyDone
	c.mu.Unlock()
	select {
	case <-copyDone:
		return c.completedError()
	case <-time.After(timeout):
		return errProcessRetryOutputDrainTimedOut
	}
}

func finishProcessRetryOutputCapturesAfterWait(timeout time.Duration, captures ...*processRetryOutputCapture) error {
	errCh := make(chan error, len(captures))
	for _, capture := range captures {
		go func() {
			errCh <- capture.FinishAfterWait(timeout)
		}()
	}
	var err error
	for range captures {
		err = errors.Join(err, <-errCh)
	}
	return err
}

func (c *processRetryOutputCapture) AbortAfterReapedChild(timeout time.Duration) error {
	return c.abort(timeout)
}

func (c *processRetryOutputCapture) AbortAfterUnreaped(timeout time.Duration) error {
	return c.abort(timeout)
}

func (c *processRetryOutputCapture) abort(timeout time.Duration) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.finished {
		err := c.copyErr
		c.mu.Unlock()
		return err
	}
	alreadyAborted := c.aborted
	c.aborted = true
	copyStarted := c.copyStarted
	copyDone := c.copyDone
	var writePipe, readPipe *os.File
	if !alreadyAborted {
		writePipe = c.writePipe
		readPipe = c.readPipe
		c.writePipe = nil
		c.readPipe = nil
	}
	c.mu.Unlock()

	var closeErr error
	if writePipe != nil {
		closeErr = errors.Join(closeErr, ignoreProcessRetryClosedError(writePipe.Close()))
	}
	if readPipe != nil {
		closeErr = errors.Join(closeErr, ignoreProcessRetryClosedError(readPipe.Close()))
	}
	if !copyStarted {
		c.complete(closeErr)
		return closeErr
	}
	if timeout <= 0 {
		select {
		case <-copyDone:
			return errors.Join(closeErr, c.completedError())
		default:
			return closeErr
		}
	}
	select {
	case <-copyDone:
		return errors.Join(closeErr, c.completedError())
	case <-time.After(timeout):
		return errors.Join(closeErr, errors.New("process retry output capture abort timed out"))
	}
}

func (c *processRetryOutputCapture) CloseSetupFailure() error {
	return c.abort(0)
}

func (c *processRetryOutputCapture) Tail() (string, bool, error) {
	if c == nil {
		return "", false, nil
	}
	c.mu.Lock()
	sink := c.sink
	aborted := c.aborted
	c.mu.Unlock()
	if sink == nil {
		return "", aborted, nil
	}
	tail, truncated := sink.Tail()
	return tail, truncated || aborted, nil
}

func combineProcessRetryOutputTails(stdout, stderr *processRetryOutputCapture, maxBytes int64) (string, bool, error) {
	stdoutTail, stdoutTruncated, stdoutErr := stdout.Tail()
	stderrTail, stderrTruncated, stderrErr := stderr.Tail()
	combined := stdoutTail
	if stderrTail != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += stderrTail
	}
	truncated := stdoutTruncated || stderrTruncated
	if maxBytes >= 0 && int64(len(combined)) > maxBytes {
		combined = combined[len(combined)-int(maxBytes):]
		truncated = true
	}
	if truncated {
		combined = processRetryOutputTruncationMarker + combined
	}
	return combined, truncated, errors.Join(stdoutErr, stderrErr)
}

type processRetryAttemptResult struct {
	Result               processRetryResult
	TempDir              string
	OutputTail           string
	OutputTruncated      bool
	ExitCode             int
	ExitStatusObserved   bool
	StartTime            time.Time
	FinishTime           time.Time
	Err                  error
	CaptureErr           error
	TimedOut             bool
	Unreaped             bool
	ContainmentLost      bool
	SetupFailure         bool
	SetupFailureConsumed bool
	SetupFallbackAllowed bool
	Cleanup              func()
}

type processRetryEffectiveStatus struct {
	Status      processRetryStatus
	Failed      bool
	Skipped     bool
	FailureKind string
}

type processRetryMetadataSnapshot struct {
	identity                      *testIdentity
	isANewTest                    bool
	isAModifiedTest               bool
	isEarlyFlakeDetectionEnabled  bool
	isFlakyTestRetriesEnabled     bool
	isItrForcedRun                bool
	isQuarantined                 bool
	isDisabled                    bool
	isAttemptToFix                bool
	hasAdditionalFeatureWrapper   bool
	hasExplicitQuarantined        bool
	hasExplicitDisabled           bool
	hasExplicitAttemptToFix       bool
	suppressParentRetryMetadata   bool
	shouldOrchestrateAttemptToFix bool
}

type processRetryLaunchBaseline struct {
	hooks            processRetryRunnerHooks
	executable       string
	workingDirectory string
	args             []string
	argsSnapshot     processRetryArgsSnapshot
	environment      []string
	currentCPU       int
	timeout          time.Duration
	timeoutSet       bool
	err              error
}

type processRetryArgsSnapshot struct {
	captured     bool
	preserved    []string
	boundary     []string
	runSelector  string
	skipSelector string
	timeout      time.Duration
	timeoutSet   bool
	ok           bool
	reason       string
}

type processRetryIterationOutcome int

const (
	processRetryIterationFallback processRetryIterationOutcome = iota
	processRetryIterationStop
	processRetryIterationContinue
)

type processRetryTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type processRetryRunnerHooks struct {
	executable       func() (string, error)
	workingDirectory func() (string, error)
	args             func() []string
	environ          func() []string
	command          func(executable string, args ...string) *exec.Cmd
	prepareTree      func(cmd *exec.Cmd) error
	startAndWait     func(cmd *exec.Cmd) (<-chan error, error)
	attachTree       func(cmd *exec.Cmd) error
	resumeTree       func(cmd *exec.Cmd) error
	terminateTree    func(cmd *exec.Cmd) error
	killTree         func(cmd *exec.Cmd) error
	killDirect       func(cmd *exec.Cmd) error
	releaseTree      func(cmd *exec.Cmd) error
	now              func() time.Time
	after            func(time.Duration) <-chan time.Time
	newTimer         func(time.Duration) processRetryTimer
	removeAll        func(string) error
	outputDrainWait  time.Duration
	startsSuspended  bool
}

type processRetryRealTimer struct {
	timer *time.Timer
}

func (t *processRetryRealTimer) C() <-chan time.Time { return t.timer.C }
func (t *processRetryRealTimer) Stop() bool          { return t.timer.Stop() }

type processRetryLimiter struct {
	once sync.Once
	ch   chan struct{}
}

type processRetryLimiterAcquireCause string

const (
	processRetryLimiterAcquired       processRetryLimiterAcquireCause = "acquired"
	processRetryLimiterExternalCancel processRetryLimiterAcquireCause = "external_cancel"
	processRetryLimiterParentDeadline processRetryLimiterAcquireCause = "parent_deadline"
	processRetryLimiterShutdown       processRetryLimiterAcquireCause = "shutdown"
)

type processRetryLimiterRelease func()

type processRetryLimiterAcquireResult struct {
	Cause   processRetryLimiterAcquireCause
	Err     error
	Release processRetryLimiterRelease
}

type processRetryLaunchGateState struct {
	mu             locking.Mutex
	disabled       atomic.Bool
	reaping        int
	launching      int
	activeGroups   int
	activeChildren int
	shuttingDown   bool
	shutdown       chan struct{}
	changed        chan struct{}
}

type processRetryReapPhase struct {
	started  atomic.Bool
	finished atomic.Bool
}

type processRetryActiveChild struct {
	cmd                *exec.Cmd
	killTree           func(*exec.Cmd) error
	killDirect         func(*exec.Cmd) error
	shutdownKillIssued bool
}

var globalProcessRetryLimiter atomic.Pointer[processRetryLimiter]
var processRetryRunnerHooksOverride atomic.Pointer[processRetryRunnerHooks]
var processRetryLaunchGate = processRetryLaunchGateState{
	shutdown: make(chan struct{}),
	changed:  make(chan struct{}),
}
var processRetryActiveChildren = struct {
	mu                     locking.Mutex
	children               map[*exec.Cmd]processRetryActiveChild
	closeActionRegistered  bool
	closeActionRegistering bool
	closeActionChanged     chan struct{}
}{children: make(map[*exec.Cmd]processRetryActiveChild)}

func defaultProcessRetryRunnerHooks() processRetryRunnerHooks {
	return processRetryRunnerHooks{
		executable:       os.Executable,
		workingDirectory: os.Getwd,
		args: func() []string {
			return os.Args[1:]
		},
		environ:     os.Environ,
		command:     exec.Command,
		prepareTree: setProcessGroupForCommand,
		startAndWait: func(cmd *exec.Cmd) (<-chan error, error) {
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			waitCh := make(chan error, 1)
			go func() {
				waitCh <- cmd.Wait()
			}()
			return waitCh, nil
		},
		attachTree:    attachProcessTree,
		resumeTree:    resumeProcessTree,
		terminateTree: terminateProcessTree,
		killTree:      killProcessTree,
		killDirect:    killDirectChild,
		releaseTree:   releaseProcessTree,
		now:           time.Now,
		after:         time.After,
		newTimer: func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		},
		removeAll:       os.RemoveAll,
		outputDrainWait: processRetryOutputDrainWait,
		startsSuspended: processRetryChildStartsSuspended(),
	}
}

func currentProcessRetryRunnerHooks() processRetryRunnerHooks {
	if hooks := processRetryRunnerHooksOverride.Load(); hooks != nil {
		return resolveProcessRetryRunnerHooks(*hooks)
	}
	return defaultProcessRetryRunnerHooks()
}

func resolveProcessRetryRunnerHooks(resolved processRetryRunnerHooks) processRetryRunnerHooks {
	if resolved.prepareTree == nil {
		resolved.prepareTree = noopProcessRetryTree
	}
	if resolved.attachTree == nil {
		resolved.attachTree = noopProcessRetryTree
	}
	if resolved.resumeTree == nil {
		resolved.resumeTree = noopProcessRetryTree
	}
	if resolved.terminateTree == nil {
		resolved.terminateTree = noopProcessRetryTree
	}
	if resolved.killTree == nil {
		resolved.killTree = noopProcessRetryTree
	}
	if resolved.killDirect == nil {
		resolved.killDirect = noopProcessRetryTree
	}
	if resolved.releaseTree == nil {
		resolved.releaseTree = noopProcessRetryTree
	}
	if resolved.now == nil {
		resolved.now = time.Now
	}
	if resolved.after == nil {
		resolved.after = time.After
	}
	if resolved.newTimer == nil {
		resolved.newTimer = func(d time.Duration) processRetryTimer {
			return &processRetryRealTimer{timer: time.NewTimer(d)}
		}
	}
	if resolved.removeAll == nil {
		resolved.removeAll = os.RemoveAll
	}
	if resolved.outputDrainWait <= 0 {
		resolved.outputDrainWait = processRetryOutputDrainWait
	}
	return resolved
}

func noopProcessRetryTree(*exec.Cmd) error { return nil }

func killDirectChild(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return errProcessRetryProcessNotStarted
	}
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func captureProcessRetryLaunchBaseline() *processRetryLaunchBaseline {
	hooks := currentProcessRetryRunnerHooks()
	baseline := &processRetryLaunchBaseline{hooks: hooks}
	baseline.executable, baseline.err = hooks.executable()
	if baseline.err != nil {
		return baseline
	}
	baseline.workingDirectory, baseline.err = hooks.workingDirectory()
	if baseline.err != nil {
		return baseline
	}
	baseline.args = append([]string(nil), hooks.args()...)
	baseline.argsSnapshot = captureProcessRetryArgsSnapshot(baseline.args)
	baseline.environment = append([]string(nil), hooks.environ()...)
	baseline.currentCPU = processRetryCurrentCPU()
	baseline.timeout, baseline.timeoutSet = processRetryTimeoutFromEnv()
	getProcessRetryLimiter().init()
	return baseline
}

func resetProcessRetryRunnerHooksForTesting(t testing.TB, hooks processRetryRunnerHooks) {
	t.Helper()
	old := processRetryRunnerHooksOverride.Swap(&hooks)
	t.Cleanup(func() {
		processRetryRunnerHooksOverride.Store(old)
	})
}

func processRetryLaunchesDisabled() bool {
	return processRetryLaunchGate.disabled.Load()
}

func processRetryShuttingDown() bool {
	processRetryLaunchGate.mu.Lock()
	defer processRetryLaunchGate.mu.Unlock()
	return processRetryLaunchGate.shuttingDown
}

func disableProcessRetryLaunches() {
	processRetryLaunchGate.mu.Lock()
	processRetryLaunchGate.disabled.Store(true)
	processRetryLaunchGate.notifyLocked()
	processRetryLaunchGate.mu.Unlock()
}

func beginProcessRetryGroup() (<-chan struct{}, func(), error) {
	if !processRetryShutdownActionRegistered() {
		return nil, nil, errProcessRetryShutdown
	}
	processRetryLaunchGate.mu.Lock()
	defer processRetryLaunchGate.mu.Unlock()
	processRetryLaunchGate.ensureChannelsLocked()
	if processRetryLaunchGate.shuttingDown {
		return processRetryLaunchGate.shutdown, nil, errProcessRetryShutdown
	}
	if processRetryLaunchGate.disabled.Load() {
		return processRetryLaunchGate.shutdown, nil, errProcessRetryLaunchDisabled
	}
	processRetryLaunchGate.activeGroups++
	shutdown := processRetryLaunchGate.shutdown
	var once sync.Once
	finish := func() {
		once.Do(func() {
			processRetryLaunchGate.mu.Lock()
			processRetryLaunchGate.activeGroups--
			processRetryLaunchGate.notifyLocked()
			processRetryLaunchGate.mu.Unlock()
		})
	}
	return shutdown, finish, nil
}

func beginProcessRetryShutdown() {
	processRetryLaunchGate.mu.Lock()
	processRetryLaunchGate.beginShutdownLocked()
	processRetryLaunchGate.mu.Unlock()
}

func waitForProcessRetryShutdownQuiescence(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		processRetryLaunchGate.mu.Lock()
		if processRetryLaunchGate.activeGroups == 0 &&
			processRetryLaunchGate.launching == 0 &&
			processRetryLaunchGate.activeChildren == 0 {
			processRetryLaunchGate.mu.Unlock()
			return true
		}
		changed := processRetryLaunchGate.changed
		processRetryLaunchGate.mu.Unlock()
		select {
		case <-changed:
		case <-timer.C:
			return false
		}
	}
}

func (g *processRetryLaunchGateState) ensureChannelsLocked() {
	if g.shutdown == nil {
		g.shutdown = make(chan struct{})
	}
	if g.changed == nil {
		g.changed = make(chan struct{})
	}
}

func (g *processRetryLaunchGateState) beginShutdownLocked() {
	g.ensureChannelsLocked()
	if !g.shuttingDown {
		g.shuttingDown = true
		close(g.shutdown)
	}
	g.notifyLocked()
}

func beginProcessRetryReapPhase() *processRetryReapPhase {
	phase := &processRetryReapPhase{}
	phase.begin()
	return phase
}

func (p *processRetryReapPhase) begin() {
	if p == nil || !p.started.CompareAndSwap(false, true) {
		return
	}
	processRetryLaunchGate.mu.Lock()
	processRetryLaunchGate.reaping++
	processRetryLaunchGate.mu.Unlock()
}

func (p *processRetryReapPhase) finish(containmentLost bool) {
	if p == nil || !p.started.Load() || !p.finished.CompareAndSwap(false, true) {
		return
	}
	processRetryLaunchGate.mu.Lock()
	processRetryLaunchGate.reaping--
	if containmentLost {
		processRetryLaunchGate.disabled.Store(true)
	}
	processRetryLaunchGate.notifyLocked()
	processRetryLaunchGate.mu.Unlock()
}

func processRetryShutdownActionRegistered() bool {
	processRetryActiveChildren.mu.Lock()
	defer processRetryActiveChildren.mu.Unlock()
	return processRetryActiveChildren.closeActionRegistered
}

func registerProcessRetryShutdownAction() bool {
	for {
		processRetryActiveChildren.mu.Lock()
		if processRetryActiveChildren.closeActionRegistered {
			processRetryActiveChildren.mu.Unlock()
			return true
		}
		if processRetryActiveChildren.closeActionRegistering {
			changed := processRetryActiveChildren.closeActionChanged
			processRetryActiveChildren.mu.Unlock()
			<-changed
			continue
		}
		processRetryActiveChildren.closeActionRegistering = true
		processRetryActiveChildren.closeActionChanged = make(chan struct{})
		changed := processRetryActiveChildren.closeActionChanged
		processRetryActiveChildren.mu.Unlock()

		registered := integrations.TryPushCiVisibilityPreCloseAction(stopActiveProcessRetryChildren)
		processRetryActiveChildren.mu.Lock()
		processRetryActiveChildren.closeActionRegistered = registered
		processRetryActiveChildren.closeActionRegistering = false
		close(changed)
		processRetryActiveChildren.closeActionChanged = nil
		processRetryActiveChildren.mu.Unlock()
		return registered
	}
}

func registerActiveProcessRetryChild(cmd *exec.Cmd, hooks processRetryRunnerHooks) {
	if cmd == nil {
		return
	}
	processRetryLaunchGate.mu.Lock()
	registerActiveProcessRetryChildLocked(cmd, hooks)
	processRetryLaunchGate.mu.Unlock()
}

func registerActiveProcessRetryChildLocked(cmd *exec.Cmd, hooks processRetryRunnerHooks) {
	processRetryActiveChildren.mu.Lock()
	if _, exists := processRetryActiveChildren.children[cmd]; exists {
		processRetryActiveChildren.mu.Unlock()
		return
	}
	processRetryActiveChildren.children[cmd] = processRetryActiveChild{
		cmd:        cmd,
		killTree:   hooks.killTree,
		killDirect: hooks.killDirect,
	}
	processRetryActiveChildren.mu.Unlock()
	processRetryLaunchGate.activeChildren++
	processRetryLaunchGate.notifyLocked()
}

func unregisterActiveProcessRetryChild(cmd *exec.Cmd) {
	processRetryLaunchGate.mu.Lock()
	processRetryActiveChildren.mu.Lock()
	if _, exists := processRetryActiveChildren.children[cmd]; exists {
		delete(processRetryActiveChildren.children, cmd)
		processRetryLaunchGate.activeChildren--
		processRetryLaunchGate.notifyLocked()
	}
	processRetryActiveChildren.mu.Unlock()
	processRetryLaunchGate.mu.Unlock()
}

func stopActiveProcessRetryChildren() {
	beginProcessRetryShutdown()
	processRetryActiveChildren.mu.Lock()
	children := make([]processRetryActiveChild, 0, len(processRetryActiveChildren.children))
	for cmd, child := range processRetryActiveChildren.children {
		if !child.shutdownKillIssued {
			children = append(children, child)
			child.shutdownKillIssued = true
			processRetryActiveChildren.children[cmd] = child
		}
	}
	processRetryActiveChildren.closeActionRegistered = false
	processRetryActiveChildren.mu.Unlock()
	for _, child := range children {
		if err := errors.Join(child.killTree(child.cmd), child.killDirect(child.cmd)); err != nil {
			log.Debug("civisibility: failed to stop active process retry child: %v", err.Error())
		}
	}
	if !waitForProcessRetryShutdownQuiescence(processRetryShutdownWait) {
		log.Debug("civisibility: timed out waiting for process retry groups during shutdown")
	}
}

func startProcessRetryChild(
	ctx context.Context,
	parentDeadlineHardCap <-chan time.Time,
	hooks processRetryRunnerHooks,
	cmd *exec.Cmd,
) (<-chan error, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(errProcessRetryLaunchCanceled, err)
		}

		processRetryLaunchGate.mu.Lock()
		if processRetryLaunchGate.shuttingDown {
			processRetryLaunchGate.mu.Unlock()
			return nil, errProcessRetryShutdown
		}
		if processRetryLaunchGate.disabled.Load() {
			processRetryLaunchGate.mu.Unlock()
			return nil, errProcessRetryLaunchDisabled
		}
		if processRetryLaunchGate.reaping == 0 {
			select {
			case <-parentDeadlineHardCap:
				processRetryLaunchGate.mu.Unlock()
				if err := ctx.Err(); err != nil {
					return nil, errors.Join(errProcessRetryLaunchCanceled, err)
				}
				return nil, errors.Join(errProcessRetryLaunchDeadline, context.DeadlineExceeded)
			default:
			}
			if err := ctx.Err(); err != nil {
				processRetryLaunchGate.mu.Unlock()
				return nil, errors.Join(errProcessRetryLaunchCanceled, err)
			}
			processRetryLaunchGate.launching++
			processRetryLaunchGate.notifyLocked()
			processRetryLaunchGate.mu.Unlock()

			waitCh, startErr := hooks.startAndWait(cmd)
			processRetryLaunchGate.mu.Lock()
			processRetryLaunchGate.launching--
			started := startErr == nil || waitCh != nil
			if started && hooks.killTree != nil && hooks.killDirect != nil {
				registerActiveProcessRetryChildLocked(cmd, hooks)
			}
			resultErr := startErr
			switch {
			case processRetryLaunchGate.shuttingDown:
				resultErr = errors.Join(errProcessRetryShutdown, startErr)
			case processRetryLaunchGate.disabled.Load():
				resultErr = errors.Join(errProcessRetryLaunchDisabled, startErr)
			case ctx.Err() != nil:
				resultErr = errors.Join(errProcessRetryLaunchCanceled, ctx.Err(), startErr)
			default:
				select {
				case <-parentDeadlineHardCap:
					resultErr = errors.Join(errProcessRetryLaunchDeadline, context.DeadlineExceeded, startErr)
				default:
				}
			}
			processRetryLaunchGate.notifyLocked()
			processRetryLaunchGate.mu.Unlock()
			return waitCh, resultErr
		}
		changed := processRetryLaunchGate.changed
		processRetryLaunchGate.mu.Unlock()

		select {
		case <-changed:
			continue
		case <-ctx.Done():
			return nil, errors.Join(errProcessRetryLaunchCanceled, ctx.Err())
		case <-parentDeadlineHardCap:
			if err := ctx.Err(); err != nil {
				return nil, errors.Join(errProcessRetryLaunchCanceled, err)
			}
			return nil, errors.Join(errProcessRetryLaunchDeadline, context.DeadlineExceeded)
		}
	}
}

func resetProcessRetryLaunchGateForTesting(t testing.TB) func() {
	t.Helper()
	processRetryLaunchGate.mu.Lock()
	oldDisabled := processRetryLaunchGate.disabled.Load()
	oldReaping := processRetryLaunchGate.reaping
	oldLaunching := processRetryLaunchGate.launching
	oldActiveGroups := processRetryLaunchGate.activeGroups
	oldActiveChildren := processRetryLaunchGate.activeChildren
	oldShuttingDown := processRetryLaunchGate.shuttingDown
	oldShutdown := processRetryLaunchGate.shutdown
	processRetryLaunchGate.disabled.Store(false)
	processRetryLaunchGate.reaping = 0
	processRetryLaunchGate.launching = 0
	processRetryLaunchGate.activeGroups = 0
	processRetryLaunchGate.activeChildren = 0
	processRetryLaunchGate.shuttingDown = false
	processRetryLaunchGate.shutdown = make(chan struct{})
	processRetryLaunchGate.notifyLocked()
	processRetryLaunchGate.mu.Unlock()
	return func() {
		processRetryLaunchGate.mu.Lock()
		processRetryLaunchGate.disabled.Store(oldDisabled)
		processRetryLaunchGate.reaping = oldReaping
		processRetryLaunchGate.launching = oldLaunching
		processRetryLaunchGate.activeGroups = oldActiveGroups
		processRetryLaunchGate.activeChildren = oldActiveChildren
		processRetryLaunchGate.shuttingDown = oldShuttingDown
		processRetryLaunchGate.shutdown = oldShutdown
		processRetryLaunchGate.notifyLocked()
		processRetryLaunchGate.mu.Unlock()
	}
}

func (g *processRetryLaunchGateState) notifyLocked() {
	g.ensureChannelsLocked()
	close(g.changed)
	g.changed = make(chan struct{})
}

func getProcessRetryLimiter() *processRetryLimiter {
	if limiter := globalProcessRetryLimiter.Load(); limiter != nil {
		return limiter
	}
	limiter := &processRetryLimiter{}
	if globalProcessRetryLimiter.CompareAndSwap(nil, limiter) {
		return limiter
	}
	return globalProcessRetryLimiter.Load()
}

func (l *processRetryLimiter) init() {
	l.once.Do(func() {
		capacity := processRetryMaxConcurrencyFromEnv(1)
		l.ch = make(chan struct{}, capacity)
		log.Debug("civisibility: process retry child concurrency limiter initialized with capacity %d", capacity)
	})
}

func (l *processRetryLimiter) acquire(ctx context.Context, parentDeadlineHardCap <-chan time.Time) processRetryLimiterAcquireResult {
	return l.acquireWithShutdown(ctx, parentDeadlineHardCap, nil)
}

func (l *processRetryLimiter) acquireWithShutdown(
	ctx context.Context,
	parentDeadlineHardCap <-chan time.Time,
	shutdown <-chan struct{},
) processRetryLimiterAcquireResult {
	if ctx == nil {
		ctx = context.Background()
	}
	l.init()
	if processRetryShutdownRequested(shutdown) {
		return processRetryLimiterAcquireResult{Cause: processRetryLimiterShutdown, Err: errProcessRetryShutdown}
	}
	if err := ctx.Err(); err != nil {
		return processRetryLimiterAcquireResult{Cause: processRetryLimiterExternalCancel, Err: err}
	}
	select {
	case l.ch <- struct{}{}:
		var releaseOnce sync.Once
		release := func() {
			releaseOnce.Do(func() {
				<-l.ch
			})
		}
		if err := ctx.Err(); err != nil {
			release()
			return processRetryLimiterAcquireResult{Cause: processRetryLimiterExternalCancel, Err: err}
		}
		if processRetryShutdownRequested(shutdown) {
			release()
			return processRetryLimiterAcquireResult{Cause: processRetryLimiterShutdown, Err: errProcessRetryShutdown}
		}
		return processRetryLimiterAcquireResult{Cause: processRetryLimiterAcquired, Release: release}
	case <-ctx.Done():
		return processRetryLimiterAcquireResult{Cause: processRetryLimiterExternalCancel, Err: ctx.Err()}
	case <-shutdown:
		return processRetryLimiterAcquireResult{Cause: processRetryLimiterShutdown, Err: errProcessRetryShutdown}
	case <-parentDeadlineHardCap:
		if err := ctx.Err(); err != nil {
			return processRetryLimiterAcquireResult{Cause: processRetryLimiterExternalCancel, Err: err}
		}
		return processRetryLimiterAcquireResult{Cause: processRetryLimiterParentDeadline, Err: context.DeadlineExceeded}
	}
}

func processRetryShutdownRequested(shutdown <-chan struct{}) bool {
	select {
	case <-shutdown:
		return true
	default:
		return false
	}
}

func resetProcessRetryLimiterForTesting(t testing.TB) {
	t.Helper()
	old := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	t.Cleanup(func() {
		globalProcessRetryLimiter.Store(old)
	})
}

func processRetryParentDeadlineReserve() time.Duration {
	return processRetryKillGracePeriod + processRetryPostKillWait + processRetryOutputDrainBudget + processRetryParentDeadlineSafetyMargin
}

func runProcessRetryAttempt(ctx context.Context, cfg processRetryChildConfig, parentDeadline time.Time, parentDeadlineOK bool) processRetryAttemptResult {
	return runProcessRetryAttemptWithBaseline(ctx, cfg, parentDeadline, parentDeadlineOK, captureProcessRetryLaunchBaseline())
}

func runProcessRetryAttemptWithBaseline(
	ctx context.Context,
	cfg processRetryChildConfig,
	parentDeadline time.Time,
	parentDeadlineOK bool,
	baseline *processRetryLaunchBaseline,
) processRetryAttemptResult {
	return runProcessRetryAttemptWithBaselineAndShutdown(ctx, cfg, parentDeadline, parentDeadlineOK, baseline, nil)
}

func runProcessRetryAttemptWithBaselineAndShutdown(
	ctx context.Context,
	cfg processRetryChildConfig,
	parentDeadline time.Time,
	parentDeadlineOK bool,
	baseline *processRetryLaunchBaseline,
	shutdown <-chan struct{},
) processRetryAttemptResult {
	if ctx == nil {
		ctx = context.Background()
	}
	parentStart := time.Now()
	attempt := processRetryAttemptResult{
		ExitCode:  processRetryExitCodeUnset,
		StartTime: parentStart,
	}
	finishSetupFailure := func(err error, fallbackAllowed bool, timedOut bool) processRetryAttemptResult {
		attempt.SetupFailure = true
		attempt.SetupFallbackAllowed = fallbackAllowed
		attempt.TimedOut = timedOut
		attempt.Err = err
		attempt.FinishTime = time.Now()
		return attempt
	}
	if processRetryShutdownRequested(shutdown) || processRetryShuttingDown() {
		return finishSetupFailure(errProcessRetryShutdown, false, false)
	}
	if processRetryLaunchesDisabled() {
		return finishSetupFailure(errProcessRetryLaunchDisabled, true, false)
	}
	if baseline == nil {
		baseline = captureProcessRetryLaunchBaseline()
	}
	if baseline.err != nil {
		return finishSetupFailure(baseline.err, true, false)
	}
	hooks := resolveProcessRetryRunnerHooks(baseline.hooks)
	executable := baseline.executable
	workingDir := baseline.workingDirectory
	argsSnapshot := baseline.argsSnapshot
	if !argsSnapshot.captured {
		argsSnapshot = captureProcessRetryArgsSnapshot(baseline.args)
	}
	if !argsSnapshot.ok {
		return finishSetupFailure(errors.New(argsSnapshot.reason), true, false)
	}
	currentCPU := baseline.currentCPU
	if currentCPU < 1 {
		currentCPU = processRetryCurrentCPU()
	}
	selectedTimeout := selectedProcessRetryTimeout(argsSnapshot.timeout, argsSnapshot.timeoutSet, baseline.timeout, baseline.timeoutSet, parentDeadline, parentDeadlineOK, hooks.now())
	if selectedTimeout <= 0 {
		return finishSetupFailure(context.DeadlineExceeded, true, true)
	}
	preliminaryChildTestingTimeout := selectedTimeout + processRetryParentDeadlineReserve()
	if _, ok, reason := buildProcessRetryArgsFromSnapshot(argsSnapshot, cfg.TestName, currentCPU, preliminaryChildTestingTimeout); !ok {
		return finishSetupFailure(errors.New(reason), true, false)
	}
	if err := ctx.Err(); err != nil {
		return finishSetupFailure(err, false, false)
	}
	var parentDeadlineHardCap <-chan time.Time
	var parentDeadlineTimer processRetryTimer
	if parentDeadlineOK {
		parentDeadlineRemaining := parentDeadline.Sub(hooks.now()) - processRetryParentDeadlineReserve()
		if parentDeadlineRemaining <= 0 {
			return finishSetupFailure(context.DeadlineExceeded, true, true)
		}
		parentDeadlineTimer = hooks.newTimer(parentDeadlineRemaining)
		parentDeadlineHardCap = parentDeadlineTimer.C()
		defer parentDeadlineTimer.Stop()
	}
	limiterResult := getProcessRetryLimiter().acquireWithShutdown(ctx, parentDeadlineHardCap, shutdown)
	if limiterResult.Cause != processRetryLimiterAcquired {
		fallbackAllowed := limiterResult.Cause == processRetryLimiterParentDeadline
		return finishSetupFailure(limiterResult.Err, fallbackAllowed, fallbackAllowed)
	}
	defer limiterResult.Release()
	if processRetryShutdownRequested(shutdown) || processRetryShuttingDown() {
		return finishSetupFailure(errProcessRetryShutdown, false, false)
	}
	if processRetryLaunchesDisabled() {
		return finishSetupFailure(errProcessRetryLaunchDisabled, true, false)
	}
	if err := ctx.Err(); err != nil {
		return finishSetupFailure(err, false, false)
	}
	selectedTimeout = selectedProcessRetryTimeout(argsSnapshot.timeout, argsSnapshot.timeoutSet, baseline.timeout, baseline.timeoutSet, parentDeadline, parentDeadlineOK, hooks.now())
	if selectedTimeout <= 0 {
		return finishSetupFailure(context.DeadlineExceeded, true, true)
	}
	if parentDeadlineOK {
		parentRemaining := parentDeadline.Sub(hooks.now()) - processRetryParentDeadlineReserve()
		if parentRemaining < selectedTimeout {
			selectedTimeout = parentRemaining
		}
		if selectedTimeout <= 0 {
			return finishSetupFailure(context.DeadlineExceeded, true, true)
		}
	}
	attemptDeadline := hooks.now().Add(selectedTimeout)
	attemptTimer := hooks.newTimer(selectedTimeout)
	defer attemptTimer.Stop()
	remainingAttemptTime := func() time.Duration {
		return attemptDeadline.Sub(hooks.now())
	}
	attemptDeadlineReached := func() bool {
		select {
		case <-attemptTimer.C():
			return true
		default:
			return remainingAttemptTime() <= 0
		}
	}
	tempDir, err := os.MkdirTemp("", "dd-process-retry-*")
	if err != nil {
		return finishSetupFailure(err, true, false)
	}
	_ = os.Chmod(tempDir, 0o700)
	attempt.TempDir = tempDir
	var cleanupOnce sync.Once
	attempt.Cleanup = func() {
		cleanupOnce.Do(func() {
			if err := hooks.removeAll(tempDir); err != nil {
				log.Debug("civisibility: process retry cleanup failed")
			}
		})
	}

	resultPath := filepath.Join(tempDir, "result.json")
	childCfg := cfg
	childCfg.ResultPath = resultPath
	stdoutCapture, err := newProcessRetryOutputCapture(processRetryStreamMaxBytes)
	if err != nil {
		return finishSetupFailure(err, true, false)
	}
	stderrCapture, err := newProcessRetryOutputCapture(processRetryStreamMaxBytes)
	if err != nil {
		_ = stdoutCapture.CloseSetupFailure()
		return finishSetupFailure(err, true, false)
	}
	closeCapturesForSetupFailure := func() {
		_ = stdoutCapture.CloseSetupFailure()
		_ = stderrCapture.CloseSetupFailure()
	}
	if err := ctx.Err(); err != nil {
		closeCapturesForSetupFailure()
		return finishSetupFailure(err, false, false)
	}
	selectedTimeout = remainingAttemptTime()
	if selectedTimeout <= 0 || attemptDeadlineReached() {
		closeCapturesForSetupFailure()
		return finishSetupFailure(context.DeadlineExceeded, true, true)
	}
	childTestingTimeout := selectedTimeout + processRetryParentDeadlineReserve()
	filteredArgs, ok, reason := buildProcessRetryArgsFromSnapshot(argsSnapshot, cfg.TestName, currentCPU, childTestingTimeout)
	if !ok {
		closeCapturesForSetupFailure()
		return finishSetupFailure(errors.New(reason), true, false)
	}

	cmd := hooks.command(executable, filteredArgs...)
	cmd.Env = buildProcessRetryEnv(baseline.environment, childCfg)
	cmd.Dir = workingDir
	cmd.Stdin = nil
	cmd.Stdout = stdoutCapture.ChildWriter()
	cmd.Stderr = stderrCapture.ChildWriter()
	if err := hooks.prepareTree(cmd); err != nil {
		closeCapturesForSetupFailure()
		return finishSetupFailure(err, true, false)
	}
	treeReleased := false
	releaseTree := func() error {
		if treeReleased {
			return nil
		}
		treeReleased = true
		return hooks.releaseTree(cmd)
	}
	if err := ctx.Err(); err != nil {
		closeCapturesForSetupFailure()
		return finishSetupFailure(errors.Join(err, releaseTree()), false, false)
	}
	latestTimeout := remainingAttemptTime()
	if latestTimeout <= 0 || attemptDeadlineReached() {
		closeCapturesForSetupFailure()
		return finishSetupFailure(errors.Join(context.DeadlineExceeded, releaseTree()), true, true)
	}
	if latestTimeout < selectedTimeout {
		selectedTimeout = latestTimeout
		childTestingTimeout = selectedTimeout + processRetryParentDeadlineReserve()
		filteredArgs, ok, reason = buildProcessRetryArgsFromSnapshot(argsSnapshot, cfg.TestName, currentCPU, childTestingTimeout)
		if !ok {
			closeCapturesForSetupFailure()
			return finishSetupFailure(errors.Join(errors.New(reason), releaseTree()), true, false)
		}
		cmd.Args = append([]string{executable}, filteredArgs...)
	}

	stdoutCapture.StartCopy()
	stderrCapture.StartCopy()
	waitCh, startErr := startProcessRetryChild(ctx, attemptTimer.C(), hooks, cmd)
	if startErr != nil && waitCh == nil {
		closeCapturesForSetupFailure()
		fallbackAllowed := !errors.Is(startErr, errProcessRetryLaunchCanceled) &&
			!errors.Is(startErr, errProcessRetryShutdown)
		timedOut := errors.Is(startErr, errProcessRetryLaunchDeadline)
		return finishSetupFailure(errors.Join(startErr, releaseTree()), fallbackAllowed, timedOut)
	}
	attempt.StartTime = hooks.now()
	_ = stdoutCapture.CloseParentWriter()
	_ = stderrCapture.CloseParentWriter()
	teardownPhase := &processRetryReapPhase{}
	containmentLost := false
	defer func() {
		teardownPhase.finish(containmentLost || attempt.Unreaped)
	}()
	markContainmentLost := func(err error) {
		containmentLost = true
		attempt.ContainmentLost = true
		attempt.Err = errors.Join(attempt.Err, errProcessRetryContainmentLost, err)
	}

	forceKillAndWait := func(kill func(*exec.Cmd) error) error {
		teardownPhase.begin()
		if killErr := kill(cmd); killErr != nil {
			markContainmentLost(killErr)
			if directErr := hooks.killDirect(cmd); directErr != nil {
				markContainmentLost(directErr)
			}
		}
		waitErr := waitForProcessRetryReapAfterKillWithPhase(hooks, waitCh, &attempt, teardownPhase)
		if attempt.Unreaped {
			markContainmentLost(nil)
		}
		return waitErr
	}

	var waitErr error
	if startErr != nil {
		attempt.SetupFailure = true
		attempt.SetupFallbackAllowed = false
		attempt.TimedOut = errors.Is(startErr, errProcessRetryLaunchDeadline)
		attempt.Err = errors.Join(attempt.Err, startErr)
		if hooks.startsSuspended {
			// A suspended Windows child is not contained by its Job Object until
			// attachTree succeeds. A post-start cancellation or deadline must kill
			// that direct child rather than terminating the still-empty job.
			waitErr = forceKillAndWait(hooks.killDirect)
		} else {
			abortCtx, cancelAbort := context.WithCancel(context.Background())
			cancelAbort()
			waitErr = waitProcessRetryChildWithTeardown(
				abortCtx,
				shutdown,
				hooks,
				cmd,
				waitCh,
				attemptTimer,
				&attempt,
				teardownPhase,
				markContainmentLost,
			)
		}
	} else if attachErr := hooks.attachTree(cmd); attachErr != nil {
		attempt.SetupFailure = true
		attempt.SetupFallbackAllowed = hooks.startsSuspended
		attempt.Err = errors.Join(attempt.Err, attachErr)
		waitErr = forceKillAndWait(hooks.killDirect)
		if !hooks.startsSuspended {
			markContainmentLost(nil)
		}
	} else if attemptDeadlineReached() {
		attempt.TimedOut = true
		waitErr = forceKillAndWait(hooks.killTree)
	} else if resumeErr := hooks.resumeTree(cmd); resumeErr != nil {
		attempt.SetupFailure = true
		attempt.SetupFallbackAllowed = false
		attempt.Err = errors.Join(attempt.Err, resumeErr)
		waitErr = forceKillAndWait(hooks.killTree)
	} else {
		if attemptDeadlineReached() {
			attempt.TimedOut = true
			waitErr = forceKillAndWait(hooks.killTree)
		} else {
			waitErr = waitProcessRetryChildWithTeardown(ctx, shutdown, hooks, cmd, waitCh, attemptTimer, &attempt, teardownPhase, markContainmentLost)
		}
	}
	attempt.FinishTime = hooks.now()
	attemptFromWaitError(&attempt, waitErr)
	teardownPhase.begin()
	if !attempt.Unreaped {
		// The selected test process may have exited while descendants in its
		// containment unit still hold resources or continue running.
		if killErr := hooks.killTree(cmd); killErr != nil {
			markContainmentLost(killErr)
		}
	}
	finalizeProcessRetryOutputCaptures(hooks, cmd, &attempt, stdoutCapture, stderrCapture)
	if attempt.ContainmentLost {
		containmentLost = true
	}
	if releaseErr := releaseTree(); releaseErr != nil {
		markContainmentLost(releaseErr)
	}
	if containmentLost {
		attempt.SetupFallbackAllowed = false
	}

	result, timingOK, resultErr := readProcessRetryResult(resultPath, childCfg)
	if resultErr != nil {
		attempt.Err = errors.Join(attempt.Err, resultErr)
	} else {
		attempt.Result = result
		if timingOK {
			attempt.StartTime = time.Unix(0, result.StartUnixNano)
			attempt.FinishTime = time.Unix(0, result.FinishUnixNano)
		}
	}
	if attempt.Unreaped {
		cleanup := attempt.Cleanup
		attempt.Cleanup = func() {}
		if waitCh != nil {
			go func() {
				<-waitCh
				cleanup()
				unregisterActiveProcessRetryChild(cmd)
			}()
		}
	} else {
		unregisterActiveProcessRetryChild(cmd)
	}
	return attempt
}

func finalizeProcessRetryOutputCaptures(
	hooks processRetryRunnerHooks,
	cmd *exec.Cmd,
	attempt *processRetryAttemptResult,
	stdoutCapture, stderrCapture *processRetryOutputCapture,
) {
	if attempt == nil {
		return
	}
	attempt.CaptureErr = finishProcessRetryOutputCapturesAfterWait(hooks.outputDrainWait, stdoutCapture, stderrCapture)
	if errors.Is(attempt.CaptureErr, errProcessRetryOutputDrainTimedOut) {
		attempt.ContainmentLost = true
		attempt.Err = errors.Join(attempt.Err, errProcessRetryContainmentLost)
		if killErr := hooks.killTree(cmd); killErr != nil {
			attempt.Err = errors.Join(attempt.Err, killErr)
		}
		abort := (*processRetryOutputCapture).AbortAfterReapedChild
		if attempt.Unreaped {
			abort = (*processRetryOutputCapture).AbortAfterUnreaped
		}
		attempt.CaptureErr = errors.Join(
			attempt.CaptureErr,
			abort(stdoutCapture, 0),
			abort(stderrCapture, 0),
		)
	}
	outputTail, truncated, tailErr := combineProcessRetryOutputTails(stdoutCapture, stderrCapture, processRetryOutputMaxBytes)
	attempt.OutputTail = outputTail
	attempt.OutputTruncated = truncated || attempt.CaptureErr != nil
	attempt.CaptureErr = errors.Join(attempt.CaptureErr, tailErr)
}

func selectedProcessRetryTimeout(
	argTimeout time.Duration,
	argTimeoutSet bool,
	envTimeout time.Duration,
	envTimeoutSet bool,
	parentDeadline time.Time,
	parentDeadlineOK bool,
	now time.Time,
) time.Duration {
	selected := processRetryDefaultTimeout
	if envTimeoutSet {
		selected = envTimeout
	}
	if argTimeoutSet && (selected <= 0 || argTimeout < selected) {
		selected = argTimeout
	}
	if parentDeadlineOK {
		if remaining := parentDeadline.Sub(now) - processRetryParentDeadlineReserve(); remaining < selected {
			selected = remaining
		}
	}
	return selected
}

func waitProcessRetryChild(
	ctx context.Context,
	hooks processRetryRunnerHooks,
	cmd *exec.Cmd,
	waitCh <-chan error,
	timeoutTimer processRetryTimer,
	attempt *processRetryAttemptResult,
) error {
	teardownPhase := &processRetryReapPhase{}
	containmentLost := false
	markContainmentLost := func(err error) {
		containmentLost = true
		attempt.ContainmentLost = true
		attempt.Err = errors.Join(attempt.Err, errProcessRetryContainmentLost, err)
	}
	err := waitProcessRetryChildWithTeardown(ctx, nil, hooks, cmd, waitCh, timeoutTimer, attempt, teardownPhase, markContainmentLost)
	teardownPhase.finish(containmentLost || attempt.Unreaped)
	return err
}

func waitProcessRetryChildWithTeardown(
	ctx context.Context,
	shutdown <-chan struct{},
	hooks processRetryRunnerHooks,
	cmd *exec.Cmd,
	waitCh <-chan error,
	timeoutTimer processRetryTimer,
	attempt *processRetryAttemptResult,
	teardownPhase *processRetryReapPhase,
	markContainmentLost func(error),
) error {
	observeWaitResult := func(err error) error {
		teardownPhase.begin()
		return err
	}
	drainWaitCh := func() (error, bool) {
		select {
		case err := <-waitCh:
			return observeWaitResult(err), true
		default:
			return nil, false
		}
	}
	terminateAndWait := func() error {
		if err, ok := drainWaitCh(); ok {
			return err
		}
		teardownPhase.begin()
		if terminateErr := hooks.terminateTree(cmd); terminateErr != nil {
			attempt.Err = errors.Join(attempt.Err, terminateErr)
		}
		select {
		case err := <-waitCh:
			return observeWaitResult(err)
		case <-hooks.after(processRetryKillGracePeriod):
			if killErr := hooks.killTree(cmd); killErr != nil {
				markContainmentLost(killErr)
				if directErr := hooks.killDirect(cmd); directErr != nil {
					markContainmentLost(directErr)
				}
			}
			return waitForProcessRetryReapAfterKillWithPhase(hooks, waitCh, attempt, teardownPhase)
		}
	}
	if err, ok := drainWaitCh(); ok {
		return err
	}
	select {
	case err := <-waitCh:
		return observeWaitResult(err)
	case <-ctx.Done():
		if err, ok := drainWaitCh(); ok {
			return err
		}
		return errors.Join(ctx.Err(), terminateAndWait())
	case <-shutdown:
		if err, ok := drainWaitCh(); ok {
			return err
		}
		return errors.Join(errProcessRetryShutdown, terminateAndWait())
	case <-timeoutTimer.C():
		if err, ok := drainWaitCh(); ok {
			return err
		}
		attempt.TimedOut = true
		return terminateAndWait()
	}
}

func waitForProcessRetryReapAfterKill(hooks processRetryRunnerHooks, waitCh <-chan error, attempt *processRetryAttemptResult) error {
	reapPhase := beginProcessRetryReapPhase()
	err := waitForProcessRetryReapAfterKillWithPhase(hooks, waitCh, attempt, reapPhase)
	reapPhase.finish(attempt != nil && attempt.Unreaped)
	return err
}

func waitForProcessRetryReapAfterKillWithPhase(
	hooks processRetryRunnerHooks,
	waitCh <-chan error,
	attempt *processRetryAttemptResult,
	reapPhase *processRetryReapPhase,
) error {
	if reapPhase == nil {
		reapPhase = beginProcessRetryReapPhase()
	}
	select {
	case err := <-waitCh:
		return err
	case <-hooks.after(processRetryPostKillWait):
		select {
		case err := <-waitCh:
			return err
		default:
		}
		attempt.Unreaped = true
		return errProcessRetryChildUnreaped
	}
}

func attemptFromWaitError(attempt *processRetryAttemptResult, waitErr error) {
	if waitErr == nil {
		attempt.ExitCode = 0
		attempt.ExitStatusObserved = true
		return
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		attempt.ExitCode = exitErr.ExitCode()
		attempt.ExitStatusObserved = true
		attempt.Err = errors.Join(attempt.Err, processRetryWaitErrorEvidence(waitErr))
		return
	}
	attempt.Err = errors.Join(attempt.Err, waitErr)
}

func processRetryWaitErrorEvidence(err error) error {
	var evidence error
	if errors.Is(err, context.Canceled) {
		evidence = errors.Join(evidence, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		evidence = errors.Join(evidence, context.DeadlineExceeded)
	}
	if errors.Is(err, errProcessRetryChildUnreaped) {
		evidence = errors.Join(evidence, errProcessRetryChildUnreaped)
	}
	return evidence
}

func effectiveProcessRetryStatus(attempt processRetryAttemptResult, metadataCancelled bool) processRetryEffectiveStatus {
	failed := func(kind string) processRetryEffectiveStatus {
		return processRetryEffectiveStatus{
			Status:      processRetryStatusFail,
			Failed:      true,
			FailureKind: kind,
		}
	}
	if metadataCancelled {
		return failed("metadata_cancelled")
	}
	if errors.Is(attempt.Err, errProcessRetryShutdown) {
		return failed("process_shutdown")
	}
	if attempt.Unreaped || errors.Is(attempt.Err, errProcessRetryChildUnreaped) {
		return failed("process_unreaped")
	}
	if attempt.TimedOut {
		return failed("timeout")
	}
	if errors.Is(attempt.Err, context.Canceled) || errors.Is(attempt.Err, context.DeadlineExceeded) {
		return failed("process_canceled")
	}
	if attempt.ContainmentLost || errors.Is(attempt.Err, errProcessRetryContainmentLost) {
		return failed("containment_lost")
	}
	if attempt.SetupFailure && attempt.SetupFailureConsumed {
		return failed("process_setup_failure")
	}
	if attempt.Result.Status == "" || attempt.Result.Status == processRetryStatusNotRun ||
		errors.Is(attempt.Err, errProcessRetryResultMissing) || errors.Is(attempt.Err, errProcessRetryResultInvalid) {
		return failed("missing_or_not_run")
	}
	if !attempt.ExitStatusObserved && attempt.ExitCode == processRetryExitCodeUnset {
		return failed("process_exit_unset")
	}
	if attempt.ExitStatusObserved && attempt.ExitCode == processRetryExitCodeUnset {
		return failed("process_exit")
	}
	if attempt.ExitCode != 0 && (attempt.Result.Status == processRetryStatusPass || attempt.Result.Status == processRetryStatusSkip) {
		return failed("process_exit")
	}
	if attempt.Err != nil {
		var exitErr *exec.ExitError
		if !errors.As(attempt.Err, &exitErr) {
			return failed("process_error")
		}
	}
	switch attempt.Result.Status {
	case processRetryStatusPass:
		return processRetryEffectiveStatus{Status: processRetryStatusPass}
	case processRetryStatusSkip:
		return processRetryEffectiveStatus{Status: processRetryStatusSkip, Skipped: true}
	case processRetryStatusFail:
		kind := "test_fail"
		if attempt.Result.Panic {
			kind = "test_panic"
		}
		return failed(kind)
	default:
		return failed("missing_or_not_run")
	}
}

func snapshotProcessRetryExecutionMetadata(execMeta *testExecutionMetadata) *processRetryMetadataSnapshot {
	if execMeta == nil || execMeta.identity == nil || execMeta.identity.FullName == "" {
		return nil
	}
	return &processRetryMetadataSnapshot{
		identity:                      execMeta.identity,
		isANewTest:                    execMeta.isANewTest,
		isAModifiedTest:               execMeta.isAModifiedTest,
		isEarlyFlakeDetectionEnabled:  execMeta.isEarlyFlakeDetectionEnabled,
		isFlakyTestRetriesEnabled:     execMeta.isFlakyTestRetriesEnabled,
		isItrForcedRun:                execMeta.isItrForcedRun,
		isQuarantined:                 execMeta.isQuarantined,
		isDisabled:                    execMeta.isDisabled,
		isAttemptToFix:                execMeta.isAttemptToFix,
		hasAdditionalFeatureWrapper:   execMeta.hasAdditionalFeatureWrapper,
		hasExplicitQuarantined:        execMeta.hasExplicitQuarantined,
		hasExplicitDisabled:           execMeta.hasExplicitDisabled,
		hasExplicitAttemptToFix:       execMeta.hasExplicitAttemptToFix,
		suppressParentRetryMetadata:   execMeta.suppressParentRetryMetadata,
		shouldOrchestrateAttemptToFix: execMeta.shouldOrchestrateAttemptToFix,
	}
}

func applyProcessRetryMetadataSnapshot(execMeta *testExecutionMetadata, snapshot *processRetryMetadataSnapshot) bool {
	if execMeta == nil || snapshot == nil || snapshot.identity == nil || snapshot.identity.FullName == "" {
		return false
	}
	execMeta.identity = snapshot.identity
	execMeta.isANewTest = snapshot.isANewTest
	execMeta.isAModifiedTest = snapshot.isAModifiedTest
	execMeta.isEarlyFlakeDetectionEnabled = snapshot.isEarlyFlakeDetectionEnabled
	execMeta.isFlakyTestRetriesEnabled = snapshot.isFlakyTestRetriesEnabled
	execMeta.isItrForcedRun = snapshot.isItrForcedRun
	execMeta.isQuarantined = snapshot.isQuarantined
	execMeta.isDisabled = snapshot.isDisabled
	execMeta.isAttemptToFix = snapshot.isAttemptToFix
	execMeta.hasAdditionalFeatureWrapper = snapshot.hasAdditionalFeatureWrapper
	execMeta.hasExplicitQuarantined = snapshot.hasExplicitQuarantined
	execMeta.hasExplicitDisabled = snapshot.hasExplicitDisabled
	execMeta.hasExplicitAttemptToFix = snapshot.hasExplicitAttemptToFix
	execMeta.suppressParentRetryMetadata = snapshot.suppressParentRetryMetadata
	execMeta.shouldOrchestrateAttemptToFix = snapshot.shouldOrchestrateAttemptToFix
	return true
}

func prepareProcessRetryExecution(options *runTestWithRetryOptions, execOpts *executionOptions) {
	options.processRetryMode = retryExecutionModeFromEnv()
	options.processRetryModeSet = true
	if options.processRetryMode != retryExecutionModeProcess || !options.processRetryAllowed {
		return
	}
	options.processRetryGuardsSnapshotted = true
	options.processRetryCoverageGuardSet = options.coverageActive != nil
	if options.processRetryCoverageGuardSet {
		options.processRetryCoverageActive = options.coverageActive()
	}
	options.processRetryFuzzGuardSet = options.fuzzActive != nil
	if options.processRetryFuzzGuardSet {
		options.processRetryFuzzActive = options.fuzzActive()
	}
	execOpts.processRetryLaunchBaseline = captureProcessRetryLaunchBaseline()
}

func runProcessRetriesIfEligible(
	execOpts *executionOptions,
	runSequentialRetries func(stopOnProcessShutdown bool),
) (handled bool, reason string) {
	ok, reason := processRetryEligible(execOpts.executionMetadata, execOpts.options)
	if !ok {
		if reason == "process_shutdown" {
			log.Debug("runTestWithRetry: retries stopped because CI Visibility shutdown started")
			execOpts.retryCount = 0
			return true, reason
		}
		return false, reason
	}
	execOpts.processRetryMetadataSnapshot = snapshotProcessRetryExecutionMetadata(execOpts.executionMetadata)
	if execOpts.processRetryMetadataSnapshot == nil {
		log.Debug("runTestWithRetry: process retry backend ineligible: missing_metadata_snapshot")
		runSequentialRetries(false)
		return true, "missing_metadata_snapshot"
	}
	shutdown, finishGroup, beginErr := beginProcessRetryGroup()
	switch {
	case errors.Is(beginErr, errProcessRetryShutdown):
		log.Debug("runTestWithRetry: process retry skipped because CI Visibility shutdown started")
		execOpts.retryCount = 0
	case beginErr != nil:
		log.Debug("runTestWithRetry: process retry backend unavailable before admission: %v", beginErr.Error())
		runSequentialRetries(false)
	default:
		execOpts.processRetryShutdown = shutdown
		defer finishGroup()
		log.Debug("runTestWithRetry: executing test with process retry backend")
		if runProcessRetryBackend(execOpts) {
			log.Debug("runTestWithRetry: process retry backend fallback to in_process")
			runSequentialRetries(true)
		}
	}
	return true, ""
}

func runProcessRetryBackend(execOpts *executionOptions) bool {
	firstAttempt := true
	for {
		if !firstAttempt && processRetryShutdownRequested(execOpts.processRetryShutdown) {
			execOpts.retryCount = 0
			return false
		}
		firstAttempt = false
		switch executeProcessRetryIteration(execOpts) {
		case processRetryIterationFallback:
			return true
		case processRetryIterationStop:
			return false
		case processRetryIterationContinue:
			continue
		}
	}
}

func executeProcessRetryIteration(execOpts *executionOptions) processRetryIterationOutcome {
	execOpts.mutex.Lock()
	if execOpts.executionIndex < 0 || execOpts.options == nil ||
		execOpts.options.processRetryIdentity == nil ||
		execOpts.options.processRetryIdentity.FullName == "" ||
		len(execOpts.options.processRetryIdentity.Segments) != 1 ||
		execOpts.options.preProcessRetryMetaAdjust == nil ||
		execOpts.processRetryMetadataSnapshot == nil {
		execOpts.mutex.Unlock()
		return processRetryIterationFallback
	}
	if execOpts.retryCount < 0 {
		execOpts.mutex.Unlock()
		return processRetryIterationStop
	}
	previousIndex := execOpts.executionIndex
	execOpts.executionIndex++
	currentIndex := execOpts.executionIndex

	ptrToLocalT := createNewTest()
	copyTestWithoutParent(execOpts.options.t, ptrToLocalT)
	reinitOutputWriter(ptrToLocalT)
	ptrToLocalT.Helper()
	execOpts.options.t.Helper()
	localTPrivateFields := getTestPrivateFields(ptrToLocalT)
	if localTPrivateFields == nil || localTPrivateFields.parent == nil {
		execOpts.executionIndex = previousIndex
		execOpts.mutex.Unlock()
		return processRetryIterationFallback
	}
	dummyParent := &testing.T{}
	copyTestWithoutParent(execOpts.options.t, dummyParent)
	reinitOutputWriter(dummyParent)
	*localTPrivateFields.parent = unsafe.Pointer(dummyParent)

	execMeta := createTestMetadata(ptrToLocalT, execOpts.options.t)
	execMeta.parallelForwardState = execOpts.parallelForwardState
	execMeta.hasAdditionalFeatureWrapper = true
	execMeta.isARetry = true
	if !applyProcessRetryMetadataSnapshot(execMeta, execOpts.processRetryMetadataSnapshot) {
		deleteTestMetadata(ptrToLocalT)
		execOpts.executionIndex = previousIndex
		execOpts.mutex.Unlock()
		return processRetryIterationFallback
	}
	execMeta.identity = execOpts.options.processRetryIdentity
	execMeta.isARetry = true
	execMeta.hasAdditionalFeatureWrapper = true

	parentDeadline, parentDeadlineOK := execOpts.options.t.Deadline()
	childCfg := processRetryChildConfig{
		TestName:    execOpts.options.processRetryIdentity.FullName,
		Attempt:     currentIndex,
		RetryReason: constants.AutoTestRetriesRetryReason,
	}
	ctx := context.Background()
	if execOpts.options.processRetryContext != nil {
		if injected := execOpts.options.processRetryContext(); injected != nil {
			ctx = injected
		}
	}
	execOpts.mutex.Unlock()

	attempt := runProcessRetryAttemptWithBaselineAndShutdown(
		ctx,
		childCfg,
		parentDeadline,
		parentDeadlineOK,
		execOpts.processRetryLaunchBaseline,
		execOpts.processRetryShutdown,
	)
	if attempt.Cleanup != nil {
		defer attempt.Cleanup()
	}

	execOpts.mutex.Lock()
	defer execOpts.mutex.Unlock()
	if attempt.SetupFailure && !attempt.TimedOut && !execOpts.processRetryConsumedAttempt && attempt.SetupFallbackAllowed {
		deleteTestMetadata(ptrToLocalT)
		execOpts.executionIndex = previousIndex
		return processRetryIterationFallback
	}
	if attempt.SetupFailure {
		attempt.SetupFailureConsumed = true
	}
	consumeFlakyRetryBudgetReservation(execOpts)
	execMeta.flakyRetryBudgetReservation = execOpts.flakyRetryBudgetReservation
	execOpts.options.preProcessRetryMetaAdjust(execMeta, currentIndex)
	execMeta.isLastRetry = execOpts.options.preIsLastRetry(execMeta, currentIndex, execOpts.retryCount)
	execMeta.remainingRetries = execOpts.retryCount
	terminalCancellation := errors.Is(attempt.Err, context.Canceled) || errors.Is(attempt.Err, context.DeadlineExceeded)
	terminalUnreaped := attempt.Unreaped || errors.Is(attempt.Err, errProcessRetryChildUnreaped)
	terminalContainmentLost := attempt.ContainmentLost || errors.Is(attempt.Err, errProcessRetryContainmentLost)
	terminalLaunchDisabled := errors.Is(attempt.Err, errProcessRetryLaunchDisabled)
	terminalShutdown := processRetryShutdownRequested(execOpts.processRetryShutdown) || errors.Is(attempt.Err, errProcessRetryShutdown)
	terminalAttempt := terminalCancellation || terminalUnreaped || terminalContainmentLost || terminalLaunchDisabled || terminalShutdown
	if terminalAttempt {
		execMeta.isLastRetry = true
		execMeta.remainingRetries = 0
		execOpts.retryCount = 0
	}
	execOpts.processRetryConsumedAttempt = true
	effective := closeProcessRetryTestEvent(execOpts.options.testInfo, execMeta, attempt)
	recordProcessRetryPanic(execOpts, execMeta, attempt, effective)
	switch {
	case effective.Failed:
		ptrToLocalT.Fail()
	case effective.Skipped:
		if fields := getTestPrivateFields(ptrToLocalT); fields != nil {
			fields.SetSkipped(true)
		}
	}
	if execOpts.originalExecutionMetadata != nil {
		execOpts.originalExecutionMetadata.test = execMeta.test
	}
	if execOpts.suite == nil && execMeta.test != nil {
		execOpts.suite = execMeta.test.Suite()
	}
	if execOpts.module == nil && execOpts.suite != nil {
		execOpts.module = execOpts.suite.Module()
	}
	deleteTestMetadata(ptrToLocalT)
	execOpts.retryCount--
	if execOpts.options.postPerExecution != nil {
		execOpts.options.postPerExecution(ptrToLocalT, execMeta, currentIndex, attempt.FinishTime.Sub(attempt.StartTime))
	}
	execOpts.ptrToLocalT = ptrToLocalT
	execOpts.executionMetadata = execMeta
	if terminalAttempt {
		return processRetryIterationStop
	}
	if reserveRetryBudgetIfNeeded(execOpts, ptrToLocalT, execMeta, currentIndex) {
		return processRetryIterationContinue
	}
	return processRetryIterationStop
}

func recordProcessRetryPanic(execOpts *executionOptions, execMeta *testExecutionMetadata, attempt processRetryAttemptResult, effective processRetryEffectiveStatus) {
	if execOpts == nil || execMeta == nil || execOpts.panicExecutionMetadata != nil ||
		effective.FailureKind != "test_panic" || !attempt.Result.Panic {
		return
	}
	execMeta.panicData = attempt.Result.ErrorMessage
	execMeta.panicStacktrace = attempt.Result.ErrorStack
	execOpts.panicExecutionMetadata = execMeta
}

func closeProcessRetryTestEvent(testInfo *commonInfo, execMeta *testExecutionMetadata, attempt processRetryAttemptResult) processRetryEffectiveStatus {
	if testInfo == nil {
		return processRetryEffectiveStatus{Status: processRetryStatusFail, Failed: true, FailureKind: "missing_test_info"}
	}
	module := session.GetOrCreateModule(testInfo.moduleName)
	suite := module.GetOrCreateSuite(testInfo.suiteName)
	test := suite.CreateTest(testInfo.testName, integrations.WithTestStartTime(attempt.StartTime))
	if testInfo.sourceFunc != nil {
		test.SetTestFunc(testInfo.sourceFunc)
	}
	execMeta.test = test
	cancelExecution := setTestTagsFromExecutionMetadataNoClose(test, execMeta)
	test.SetTag(constants.TestRetryExecutionMode, "process")
	if execMeta.isItrForcedRun {
		test.SetTag(constants.TestForcedToRun, "true")
		telemetry.ITRForcedRun(telemetry.TestEventType)
	}
	effective := effectiveProcessRetryStatus(attempt, cancelExecution)
	duration := max(attempt.FinishTime.Sub(attempt.StartTime), 0)
	finalExec := isFinalExecution(effective.Failed, effective.Skipped, execMeta, duration)
	if finalExec {
		if effective.FailureKind == "metadata_cancelled" {
			test.SetTag(constants.TestFinalStatus, constants.TestStatusFail)
		} else {
			finalStatus := calculateFinalStatus(
				execMeta.anyExecutionPassed || effective.Status == processRetryStatusPass,
				execMeta.anyExecutionFailed || effective.Failed,
				effective.Skipped,
				execMeta.isQuarantined,
				execMeta.isDisabled,
				execMeta.isAttemptToFix,
			)
			test.SetTag(constants.TestFinalStatus, finalStatus)
		}
	}
	if effective.Failed {
		if finalExec && execMeta.allRetriesFailed {
			test.SetTag(constants.TestHasFailedAllRetries, "true")
		}
		if (effective.FailureKind == "test_panic" || effective.FailureKind == "test_fail") && attempt.Result.ErrorType != "" {
			test.SetError(integrations.WithErrorInfo(attempt.Result.ErrorType, attempt.Result.ErrorMessage, attempt.Result.ErrorStack))
		} else if effective.FailureKind == "test_fail" {
			test.SetTag(ext.Error, true)
		} else {
			failureKind := effective.FailureKind
			if failureKind == "" {
				failureKind = "unknown"
			}
			test.SetError(integrations.WithErrorInfo(failureKind, "process retry failed: "+failureKind, ""))
		}
		suite.SetTag(ext.Error, true)
		module.SetTag(ext.Error, true)
	}
	if attempt.OutputTail != "" {
		for line := range strings.SplitSeq(attempt.OutputTail, "\n") {
			if line != "" {
				test.Log(line, "")
			}
		}
	}
	closeOpts := []integrations.TestCloseOption{integrations.WithTestFinishTime(attempt.FinishTime)}
	if effective.Skipped && attempt.Result.SkipReason != "" {
		closeOpts = append(closeOpts, integrations.WithTestSkipReason(attempt.Result.SkipReason))
	}
	switch {
	case effective.Failed:
		test.Close(integrations.ResultStatusFail, closeOpts...)
	case effective.Skipped:
		test.Close(integrations.ResultStatusSkip, closeOpts...)
	default:
		test.Close(integrations.ResultStatusPass, closeOpts...)
	}
	return effective
}

func buildProcessRetryArgs(originalArgs []string, testName string, currentCPU int, childTestingTimeout time.Duration) ([]string, bool, string) {
	return buildProcessRetryArgsFromSnapshot(captureProcessRetryArgsSnapshot(originalArgs), testName, currentCPU, childTestingTimeout)
}

func captureProcessRetryArgsSnapshot(originalArgs []string) processRetryArgsSnapshot {
	preserved, boundary, runSelector, skipSelector, ok, reason := processRetryFilterArgs(originalArgs, true)
	timeout, timeoutSet := processRetryTimeoutFromArgs(originalArgs)
	return processRetryArgsSnapshot{
		captured:     true,
		preserved:    append([]string(nil), preserved...),
		boundary:     append([]string(nil), boundary...),
		runSelector:  runSelector,
		skipSelector: skipSelector,
		timeout:      timeout,
		timeoutSet:   timeoutSet,
		ok:           ok,
		reason:       reason,
	}
}

func buildProcessRetryArgsFromSnapshot(snapshot processRetryArgsSnapshot, testName string, currentCPU int, childTestingTimeout time.Duration) ([]string, bool, string) {
	if currentCPU < 1 {
		currentCPU = 1
	}
	if childTestingTimeout <= 0 {
		return nil, false, "invalid_child_timeout"
	}
	if !snapshot.captured || !snapshot.ok {
		reason := snapshot.reason
		if reason == "" {
			reason = "invalid_args_snapshot"
		}
		return nil, false, reason
	}
	runPattern := processRetryChildRunPattern(snapshot.runSelector, testName)
	inserted := []string{
		"-test.run=" + runPattern,
	}
	if snapshot.skipSelector != "" {
		inserted = append(inserted, "-test.skip="+snapshot.skipSelector)
	}
	inserted = append(inserted,
		"-test.count=1",
		"-test.cpu="+strconv.Itoa(currentCPU),
		"-test.timeout="+childTestingTimeout.String(),
	)
	args := make([]string, 0, len(snapshot.preserved)+len(inserted)+len(snapshot.boundary))
	args = append(args, snapshot.preserved...)
	args = append(args, inserted...)
	args = append(args, snapshot.boundary...)
	return args, true, ""
}

func processRetryTimeoutFromArgs(originalArgs []string) (time.Duration, bool) {
	_, _, _, _, ok, _ := processRetryFilterArgs(originalArgs, false)
	if !ok {
		return 0, false
	}
	var timeout time.Duration
	found := false
	for i := 0; i < len(originalArgs); i++ {
		arg := originalArgs[i]
		if arg == "--" || !processRetryIsFlagToken(arg) {
			break
		}
		name, value, hasValue := processRetrySplitFlag(arg)
		if name == "" {
			break
		}
		arity, stripped := processRetryStripFlags[name]
		if stripped && arity == processRetryFlagValue && !hasValue {
			if i+1 < len(originalArgs) {
				value = originalArgs[i+1]
				i++
			}
		}
		if name != "-test.timeout" && name != "-timeout" {
			if stripped {
				continue
			}
			registered := flag.CommandLine.Lookup(strings.TrimPrefix(name, "-"))
			if registered == nil {
				if !hasValue {
					break
				}
				continue
			}
			if _, isBool := registered.Value.(processRetryBoolFlag); !hasValue && (!isBool || !registered.Value.(processRetryBoolFlag).IsBoolFlag()) {
				i++
			}
			continue
		}
		parsed, err := time.ParseDuration(value)
		if err == nil {
			if parsed > 0 {
				timeout = parsed
				found = true
			} else {
				timeout = 0
				found = false
			}
		}
	}
	return timeout, found
}

func processRetryFilterArgs(originalArgs []string, buildArgs bool) (preserved []string, boundary []string, runSelector string, skipSelector string, ok bool, reason string) {
	for i := 0; i < len(originalArgs); i++ {
		arg := originalArgs[i]
		if arg == "--" || !processRetryIsFlagToken(arg) {
			boundary = append(boundary, originalArgs[i:]...)
			return preserved, boundary, runSelector, skipSelector, true, ""
		}
		name, value, hasValue := processRetrySplitFlag(arg)
		if name == "" {
			boundary = append(boundary, originalArgs[i:]...)
			return preserved, boundary, runSelector, skipSelector, true, ""
		}
		if name == "-test.shuffle" || name == "-shuffle" {
			tokens := []string{arg}
			if !hasValue {
				if i+1 < len(originalArgs) {
					value = originalArgs[i+1]
					tokens = append(tokens, originalArgs[i+1])
					i++
				}
			}
			if value == "on" {
				return nil, nil, "", "", false, "unsupported_shuffle_on"
			}
			if buildArgs {
				preserved = append(preserved, tokens...)
			}
			continue
		}
		if arity, strip := processRetryStripFlags[name]; strip {
			if arity == processRetryFlagValue && !hasValue {
				if i+1 < len(originalArgs) {
					value = originalArgs[i+1]
					i++
				}
			}
			switch name {
			case "-test.run", "-run":
				runSelector = value
			case "-test.skip", "-skip":
				skipSelector = value
			}
			continue
		}
		registered := flag.CommandLine.Lookup(strings.TrimPrefix(name, "-"))
		if registered == nil {
			if hasValue {
				if buildArgs {
					preserved = append(preserved, arg)
				}
				continue
			}
			return nil, nil, "", "", false, "ambiguous_unknown_flag_value"
		}
		if hasValue {
			if buildArgs {
				preserved = append(preserved, arg)
			}
			continue
		}
		if boolFlag, ok := registered.Value.(processRetryBoolFlag); ok && boolFlag.IsBoolFlag() {
			if buildArgs {
				preserved = append(preserved, arg)
			}
			continue
		}
		if buildArgs {
			preserved = append(preserved, arg)
		}
		if i+1 < len(originalArgs) {
			i++
			if buildArgs {
				preserved = append(preserved, originalArgs[i])
			}
		}
	}
	return preserved, nil, runSelector, skipSelector, true, ""
}

func processRetryIsFlagToken(arg string) bool {
	return strings.HasPrefix(arg, "-") && arg != "-"
}

func processRetrySplitFlag(arg string) (name string, value string, hasValue bool) {
	if !processRetryIsFlagToken(arg) || arg == "--" {
		return "", "", false
	}
	raw := arg
	if idx := strings.Index(raw, "="); idx >= 0 {
		value = raw[idx+1:]
		raw = raw[:idx]
		hasValue = true
	}
	trimmed := strings.TrimLeft(raw, "-")
	if trimmed == "" {
		return "", "", false
	}
	return "-" + trimmed, value, hasValue
}

func processRetryChildRunPattern(originalRun, testName string) string {
	if originalRun != "" {
		return originalRun
	}
	return "^" + regexp.QuoteMeta(testName) + "$"
}

func processRetryCurrentCPU() int {
	current := runtime.GOMAXPROCS(0)
	if current < 1 {
		return 1
	}
	return current
}

func processRetryEligible(execMeta *testExecutionMetadata, options *runTestWithRetryOptions) (bool, string) {
	mode := retryExecutionModeFromEnv()
	if options != nil && options.processRetryModeSet {
		mode = options.processRetryMode
	}
	if mode != retryExecutionModeProcess {
		return false, "mode_in_process"
	}
	if isProcessRetryChild() {
		return false, "child_mode"
	}
	if options == nil {
		return false, "missing_options"
	}
	if !options.processRetryAllowed {
		return false, "process_retry_not_allowed"
	}
	if processRetryShuttingDown() {
		return false, "process_shutdown"
	}
	if processRetryLaunchesDisabled() {
		return false, "process_launch_disabled"
	}
	if options.processRetryIdentity == nil || options.processRetryIdentity.FullName == "" {
		return false, "missing_identity"
	}
	if len(options.processRetryIdentity.Segments) != 1 {
		return false, "subtest"
	}
	if options.testInfo == nil {
		return false, "missing_test_info"
	}
	if options.testInfo.testName == "" || options.testInfo.suiteName == "" || options.testInfo.moduleName == "" {
		return false, "incomplete_test_info"
	}
	if !processRetryChildCleanupSupported() {
		return false, "testing_t_layout_unsupported"
	}
	if !processRetryTestingMWorkloadsSupported() {
		return false, "testing_m_layout_unsupported"
	}
	if execMeta == nil {
		return false, "missing_execution_metadata"
	}
	if execMeta.identity == nil || execMeta.identity.FullName == "" {
		return false, "missing_execution_identity"
	}
	if execMeta.identity.FullName != options.processRetryIdentity.FullName {
		return false, "identity_mismatch"
	}
	if len(execMeta.identity.Segments) != 1 {
		return false, "subtest"
	}
	if options.preProcessRetryMetaAdjust == nil {
		return false, "missing_process_metadata_callback"
	}
	if !execMeta.isFlakyTestRetriesEnabled {
		return false, "flaky_retry_disabled"
	}
	if isAnEfdExecution(execMeta) {
		return false, "efd"
	}
	if execMeta.isAttemptToFix {
		return false, "attempt_to_fix"
	}
	if execMeta.isQuarantined {
		return false, "quarantined"
	}
	if execMeta.isDisabled {
		return false, "disabled"
	}
	coverageGuardSet := options.coverageActive != nil
	coverageActive := false
	fuzzGuardSet := options.fuzzActive != nil
	fuzzActive := false
	if options.processRetryGuardsSnapshotted {
		coverageGuardSet = options.processRetryCoverageGuardSet
		coverageActive = options.processRetryCoverageActive
		fuzzGuardSet = options.processRetryFuzzGuardSet
		fuzzActive = options.processRetryFuzzActive
	} else {
		if coverageGuardSet {
			coverageActive = options.coverageActive()
		}
		if fuzzGuardSet {
			fuzzActive = options.fuzzActive()
		}
	}
	if !coverageGuardSet {
		return false, "missing_coverage_guard"
	}
	if coverageActive {
		return false, "coverage_active"
	}
	if !fuzzGuardSet {
		return false, "missing_fuzz_guard"
	}
	if fuzzActive {
		return false, "fuzz_active"
	}
	if execMeta.isEfdInParallel {
		return false, "parallel_efd"
	}
	return true, ""
}

func runProcessRetryChild(m *testing.M) int {
	cfg, err := bootstrapProcessRetryChild()
	if err != nil {
		reason := processRetryChildConfigErrorReason(err)
		log.Debug("civisibility: process retry child config error: %s", reason)
		writeInvalidProcessRetryChildConfigResult(cfg, reason)
		return 1
	}
	finalize := instrumentProcessRetryChild(m, cfg)
	exitCode := m.Run()
	finalize(exitCode)
	return exitCode
}

type processRetryChildInstrumentationState struct {
	cfg      processRetryChildConfig
	finalize func(exitCode int)
}

var processRetryChildInstrumentations = struct {
	mu     locking.Mutex
	states map[*testing.M]*processRetryChildInstrumentationState
}{states: make(map[*testing.M]*processRetryChildInstrumentationState)}

func instrumentProcessRetryChild(m *testing.M, cfg processRetryChildConfig) func(exitCode int) {
	processRetryChildInstrumentations.mu.Lock()
	defer processRetryChildInstrumentations.mu.Unlock()
	if state := processRetryChildInstrumentations.states[m]; state != nil {
		if state.cfg != cfg {
			log.Debug("civisibility: conflicting process retry child instrumentation")
		}
		return state.finalize
	}

	writer := newProcessRetryResultWriter(cfg.ResultPath)
	var finalizeOnce sync.Once
	finalize := func(_ int) {
		finalizeOnce.Do(func() {
			writer.Write(processRetryNotRunResult(cfg, ""))
		})
	}
	processRetryChildInstrumentations.states[m] = &processRetryChildInstrumentationState{cfg: cfg, finalize: finalize}
	return configureProcessRetryChildWorkloads(
		cfg,
		writer,
		finalize,
		getInternalTestArray(m),
		getInternalBenchmarkArray(m),
		getInternalFuzzTargetArray(m),
		getInternalExampleArray(m),
		hardStopInvalidProcessRetryChild,
	)
}

func configureProcessRetryChildWorkloads(
	cfg processRetryChildConfig,
	writer *processRetryResultWriter,
	finalize func(exitCode int),
	tests *[]testing.InternalTest,
	benchmarks *[]testing.InternalBenchmark,
	fuzzTargets *[]testing.InternalFuzzTarget,
	examples *[]testing.InternalExample,
	hardStop func(reason string),
) func(exitCode int) {
	if tests == nil || benchmarks == nil || fuzzTargets == nil || examples == nil {
		writer.Write(processRetryNotRunResult(cfg, "testing_m_reflection_drift"))
		clearProcessRetryChildWorkloads(tests, benchmarks, fuzzTargets, examples)
		hardStop("testing_m_reflection_drift")
		return finalize
	}

	var selected testing.InternalTest
	found := false
	for _, test := range *tests {
		if test.Name == cfg.TestName {
			selected = test
			found = true
			break
		}
	}

	*benchmarks = nil
	*fuzzTargets = nil
	*examples = nil
	if !found {
		*tests = nil
		writer.Write(processRetryNotRunResult(cfg, ""))
		return finalize
	}

	selected.F = wrapProcessRetryChildTest(selected.F, cfg, writer)
	*tests = []testing.InternalTest{selected}
	return finalize
}

func disableProcessRetryChildExecution(m *testing.M) bool {
	tests := getInternalTestArray(m)
	benchmarks := getInternalBenchmarkArray(m)
	fuzzTargets := getInternalFuzzTargetArray(m)
	examples := getInternalExampleArray(m)
	ok := tests != nil && benchmarks != nil && fuzzTargets != nil && examples != nil
	clearProcessRetryChildWorkloads(tests, benchmarks, fuzzTargets, examples)
	return ok
}

func clearProcessRetryChildWorkloads(
	tests *[]testing.InternalTest,
	benchmarks *[]testing.InternalBenchmark,
	fuzzTargets *[]testing.InternalFuzzTarget,
	examples *[]testing.InternalExample,
) {
	if tests != nil {
		*tests = nil
	}
	if benchmarks != nil {
		*benchmarks = nil
	}
	if fuzzTargets != nil {
		*fuzzTargets = nil
	}
	if examples != nil {
		*examples = nil
	}
}

func hardStopInvalidProcessRetryChild(reason string) {
	log.Debug("civisibility: process retry child hard stop: %s", reason)
	os.Exit(1)
}

func writeInvalidProcessRetryChildConfigResult(cfg processRetryChildConfig, reason string) {
	resultPath := cfg.ResultPath
	if strings.TrimSpace(resultPath) == "" {
		var ok bool
		resultPath, ok = lookupProcessRetryChildTransport(constants.CIVisibilityInternalRetryProcessResultPath)
		if !ok || strings.TrimSpace(resultPath) == "" {
			return
		}
	}
	result := processRetryResult{
		Version:     1,
		Status:      processRetryStatusNotRun,
		ResultError: reason,
	}
	if strings.TrimSpace(cfg.TestName) != "" && cfg.Attempt > 0 && strings.TrimSpace(cfg.RetryReason) != "" {
		result = processRetryNotRunResult(cfg, reason)
	}
	if err := writeProcessRetryResultAtomically(resultPath, result); err != nil {
		log.Debug("civisibility: process retry child failed to write invalid-config result")
	}
}

type processRetryResultWriter struct {
	path string
	once sync.Once // Competing panic paths must wait for the winning write to finish.
}

func newProcessRetryResultWriter(path string) *processRetryResultWriter {
	return &processRetryResultWriter{path: path}
}

func (w *processRetryResultWriter) Write(result processRetryResult) {
	if w == nil {
		return
	}
	w.once.Do(func() {
		if strings.TrimSpace(w.path) == "" {
			return
		}
		if err := writeProcessRetryResultAtomically(w.path, result); err != nil {
			log.Debug("civisibility: process retry child failed to write result")
		}
	})
}

func processRetryNotRunResult(cfg processRetryChildConfig, resultError string) processRetryResult {
	return processRetryResult{
		Version:     1,
		TestName:    cfg.TestName,
		Attempt:     cfg.Attempt,
		RetryReason: cfg.RetryReason,
		Status:      processRetryStatusNotRun,
		ResultError: resultError,
	}
}

func wrapProcessRetryChildTest(original func(*testing.T), cfg processRetryChildConfig, writer *processRetryResultWriter) func(*testing.T) {
	return func(t *testing.T) {
		start := time.Now()
		result := processRetryResult{
			Version:       1,
			TestName:      cfg.TestName,
			Attempt:       cfg.Attempt,
			RetryReason:   cfg.RetryReason,
			StartUnixNano: start.UnixNano(),
		}
		execMeta := createTestMetadata(t, nil)
		execMeta.identity = newTestIdentity("", "", cfg.TestName)
		execMeta.test = newProcessRetryNoopTest(cfg, start, writer)
		var cleanupResult testCleanupResult
		execMeta.cleanupResult = &cleanupResult
		var bodyPanic any
		var bodyPanicStack string
		bodyReturned := false
		defer deleteTestMetadata(t)
		defer func() {
			if r := recover(); r != nil {
				bodyPanic = r
				bodyPanicStack = utils.GetStacktrace(1)
				t.Fail()
			} else if processRetryUnexpectedTestTermination(t, bodyReturned) {
				bodyPanic = unexpectedTestTerminationMessage
				bodyPanicStack = utils.GetStacktrace(1)
				t.Fail()
			}
			runProcessRetryChildCleanup(t, execMeta, &cleanupResult)
			applyTestCleanupResult(t, execMeta, &cleanupResult)
			if bodyPanic == nil && cleanupResult.panicData == nil {
				if panicInfo := execMeta.processRetryPanic.Load(); panicInfo != nil {
					t.Fail()
					result.Panic = true
					result.Failed = true
					result.ErrorType = panicInfo.Type
					result.ErrorMessage = panicInfo.Message
					result.ErrorStack = panicInfo.Stack
				}
			}
			if bodyPanic != nil {
				result.Panic = true
				result.Failed = true
				result.ErrorType = "panic"
				result.ErrorMessage = truncateProcessRetryErrorMessage(toString(bodyPanic))
				result.ErrorStack = truncateProcessRetryErrorStack(bodyPanicStack)
			}
			if cleanupResult.panicData != nil {
				t.Fail()
				result.Panic = true
				result.Failed = true
				if bodyPanic == nil {
					if cleanupResult.panicStacktrace == "" {
						cleanupResult.panicStacktrace = utils.GetStacktrace(1)
					}
					result.ErrorType = "panic"
					result.ErrorMessage = truncateProcessRetryErrorMessage(toString(cleanupResult.panicData))
					result.ErrorStack = truncateProcessRetryErrorStack(cleanupResult.panicStacktrace)
				}
			}
			finish := time.Now()
			result.FinishUnixNano = finish.UnixNano()
			result.DurationNanos = finish.Sub(start).Nanoseconds()
			result.Failed = result.Failed || t.Failed()
			result.Skipped = t.Skipped()
			if !result.Panic && result.Failed {
				if errorInfo := execMeta.processRetryError.Load(); errorInfo != nil {
					result.ErrorType = errorInfo.Type
					result.ErrorMessage = errorInfo.Message
					result.ErrorStack = errorInfo.Stack
				}
			}
			if result.Skipped && !result.Failed {
				if skipReason := execMeta.processRetrySkipReason.Load(); skipReason != nil {
					result.SkipReason = *skipReason
				}
			}
			switch {
			case result.Panic || result.Failed:
				result.Status = processRetryStatusFail
				result.SkipReason = ""
			case result.Skipped:
				result.Status = processRetryStatusSkip
			default:
				result.Status = processRetryStatusPass
			}
			writer.Write(result)
		}()

		original(t)
		bodyReturned = true
	}
}

func processRetryChildOwnerMetadata(execMeta *testExecutionMetadata) *testExecutionMetadata {
	for execMeta != nil && execMeta.processRetryOwner != nil && execMeta.processRetryOwner != execMeta {
		execMeta = execMeta.processRetryOwner
	}
	return execMeta
}

func instrumentProcessRetryChildSubtest(original func(*testing.T)) func(*testing.T) {
	return func(t *testing.T) {
		fields := getTestPrivateFields(t)
		if fields == nil || fields.parent == nil {
			original(t)
			return
		}
		owner := processRetryChildOwnerMetadata(getTestMetadataFromPointer(*fields.parent))
		if owner == nil {
			original(t)
			return
		}

		execMeta := createTestMetadata(t, nil)
		execMeta.test = owner.test
		execMeta.processRetryOwner = owner
		defer deleteTestMetadata(t)

		bodyReturned := false
		defer func() {
			panicData := recover()
			unexpectedTermination := false
			if panicData == nil && processRetryUnexpectedTestTermination(t, bodyReturned) {
				panicData = unexpectedTestTerminationMessage
				unexpectedTermination = true
			}
			if panicData == nil {
				return
			}
			t.Fail()
			owner.processRetryPanic.CompareAndSwap(nil, &processRetryErrorInfo{
				Type:    "panic",
				Message: truncateProcessRetryErrorMessage(toString(panicData)),
				Stack:   truncateProcessRetryErrorStack(utils.GetStacktrace(1)),
			})
			if unexpectedTermination {
				return
			}
			if root, ok := owner.test.(*processRetryNoopTest); ok {
				root.writePanicResult(owner.processRetryPanic.Load())
			}
			panic(panicData)
		}()

		original(t)
		bodyReturned = true
	}
}

func processRetryUnexpectedTestTermination(t *testing.T, bodyReturned bool) bool {
	if bodyReturned {
		return false
	}
	fields := getTestPrivateFields(t)
	if fields != nil && fields.finished != nil {
		return !fields.GetFinished()
	}
	return !t.Failed() && !t.Skipped()
}

func runProcessRetryChildCleanup(t *testing.T, execMeta *testExecutionMetadata, cleanupResult *testCleanupResult) {
	if cleanupResult == nil || cleanupResult.ran {
		return
	}
	if !processRetryChildCleanupSupported() {
		t.Fail()
		cleanupResult.ran = true
		cleanupResult.panicData = "process retry child cleanup unsupported"
		cleanupResult.panicStacktrace = utils.GetStacktrace(1)
		if execMeta != nil {
			execMeta.panicData = cleanupResult.panicData
			execMeta.panicStacktrace = cleanupResult.panicStacktrace
		}
		return
	}
	runTestCleanupWithOptions(t, cleanupResult, true)
}

func toString(value any) string {
	return fmt.Sprint(value)
}

func truncateProcessRetryErrorMessage(message string) string {
	return truncateProcessRetryString(message, processRetryErrorMessageMaxBytes, processRetryTruncationMarker)
}

func truncateProcessRetryErrorType(errorType string) string {
	return truncateProcessRetryString(errorType, processRetryErrorTypeMaxBytes, processRetryMetadataTruncationMarker)
}

func truncateProcessRetryErrorStack(stack string) string {
	return truncateProcessRetryString(stack, processRetryErrorStackMaxBytes, processRetryTruncationMarker)
}

func truncateProcessRetrySkipReason(reason string) string {
	return truncateProcessRetryString(reason, processRetrySkipReasonMaxBytes, processRetryMetadataTruncationMarker)
}

func truncateProcessRetryStructuredErrorMessage(message string) string {
	return truncateProcessRetryString(message, processRetryErrorMessageMaxBytes, processRetryMetadataTruncationMarker)
}

func truncateProcessRetryStructuredErrorStack(stack string) string {
	return truncateProcessRetryString(stack, processRetryErrorStackMaxBytes, processRetryMetadataTruncationMarker)
}

func truncateProcessRetryString(value string, maxBytes int, marker string) string {
	if maxBytes <= 0 {
		return ""
	}
	normalized := strings.ToValidUTF8(value, "\uFFFD")
	if normalized == value && processRetryJSONStringFits(normalized, maxBytes) {
		return normalized
	}
	if !processRetryJSONStringFits(marker, maxBytes) {
		marker = ""
	}
	runes := []rune(normalized)
	low, high := 0, len(runes)
	for low < high {
		mid := low + (high-low+1)/2
		if processRetryJSONStringFits(string(runes[:mid])+marker, maxBytes) {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[:low]) + marker
}

func processRetryJSONStringFits(value string, maxBytes int) bool {
	if len(value) > maxBytes {
		return false
	}
	payload, err := json.Marshal(value)
	return err == nil && len(payload)-2 <= maxBytes
}

func writeProcessRetryResultAtomically(resultPath string, result processRetryResult) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(payload) > processRetryResultMaxBytes {
		return errors.New("process_retry_result_too_large")
	}
	dir := filepath.Dir(resultPath)
	tmp, err := os.CreateTemp(dir, ".process-retry-result-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	return os.Rename(tmpName, resultPath)
}

func readProcessRetryResult(resultPath string, expected processRetryChildConfig) (processRetryResult, bool, error) {
	file, err := os.Open(resultPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return processRetryResult{}, false, fmt.Errorf("%w: result file missing", errProcessRetryResultMissing)
		}
		return processRetryResult{}, false, fmt.Errorf("%w: result file unreadable", errProcessRetryResultInvalid)
	}
	defer file.Close()

	payload, err := io.ReadAll(io.LimitReader(file, processRetryResultMaxBytes+1))
	if err != nil {
		return processRetryResult{}, false, fmt.Errorf("%w: result file unreadable", errProcessRetryResultInvalid)
	}
	if len(payload) > processRetryResultMaxBytes {
		return processRetryResult{}, false, fmt.Errorf("%w: result file too large", errProcessRetryResultInvalid)
	}
	var result processRetryResult
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return processRetryResult{}, false, fmt.Errorf("%w: result json invalid", errProcessRetryResultInvalid)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return processRetryResult{}, false, fmt.Errorf("%w: result json has trailing data", errProcessRetryResultInvalid)
	}
	if err := validateProcessRetryResult(result, expected); err != nil {
		return processRetryResult{}, false, err
	}
	timingOK := result.StartUnixNano != 0 && result.FinishUnixNano != 0 && result.FinishUnixNano >= result.StartUnixNano
	if timingOK && result.DurationNanos != 0 && result.DurationNanos != result.FinishUnixNano-result.StartUnixNano {
		log.Debug("civisibility: process retry result timing duration mismatch")
		timingOK = false
	}
	return result, timingOK, nil
}

func validateProcessRetryResult(result processRetryResult, expected processRetryChildConfig) error {
	if result.Version != 1 {
		return fmt.Errorf("%w: unsupported version", errProcessRetryResultInvalid)
	}
	if result.TestName != expected.TestName || result.Attempt != expected.Attempt || result.RetryReason != expected.RetryReason {
		return fmt.Errorf("%w: identity mismatch", errProcessRetryResultInvalid)
	}
	if !processRetryJSONStringFits(result.ErrorType, processRetryErrorTypeMaxBytes) ||
		!processRetryJSONStringFits(result.ErrorMessage, processRetryErrorMessageMaxBytes) ||
		!processRetryJSONStringFits(result.ErrorStack, processRetryErrorStackMaxBytes) ||
		!processRetryJSONStringFits(result.SkipReason, processRetrySkipReasonMaxBytes) ||
		!processRetryJSONStringFits(result.ResultError, processRetryResultErrorMaxBytes) {
		return fmt.Errorf("%w: metadata field too large", errProcessRetryResultInvalid)
	}
	if result.Panic && (result.Status != processRetryStatusFail || !result.Failed || result.ErrorType == "") {
		return fmt.Errorf("%w: invalid panic mirrors", errProcessRetryResultInvalid)
	}
	switch result.Status {
	case processRetryStatusPass:
		if result.Failed || result.Skipped || result.Panic || result.ErrorType != "" || result.ErrorMessage != "" || result.ErrorStack != "" || result.SkipReason != "" || result.ResultError != "" {
			return fmt.Errorf("%w: invalid pass mirrors", errProcessRetryResultInvalid)
		}
	case processRetryStatusSkip:
		if result.Failed || !result.Skipped || result.Panic || result.ErrorType != "" || result.ErrorMessage != "" || result.ErrorStack != "" || result.ResultError != "" {
			return fmt.Errorf("%w: invalid skip mirrors", errProcessRetryResultInvalid)
		}
	case processRetryStatusFail:
		if !result.Failed || result.SkipReason != "" || result.ResultError != "" || (result.ErrorType == "" && (result.ErrorMessage != "" || result.ErrorStack != "")) {
			return fmt.Errorf("%w: invalid fail mirrors", errProcessRetryResultInvalid)
		}
	case processRetryStatusNotRun:
		if result.Failed || result.Skipped || result.Panic || result.ErrorType != "" || result.ErrorMessage != "" || result.ErrorStack != "" || result.SkipReason != "" || !validProcessRetryResultError(result.ResultError) {
			return fmt.Errorf("%w: invalid not_run mirrors", errProcessRetryResultInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown status", errProcessRetryResultInvalid)
	}
	return nil
}

func validProcessRetryResultError(reason string) bool {
	switch reason {
	case "", "missing_result_path", "missing_test_name", "missing_attempt", "invalid_attempt", "missing_retry_reason", "invalid_child_config", "testing_m_reflection_drift":
		return true
	default:
		return false
	}
}

var _ integrations.Test = (*processRetryNoopTest)(nil)

type processRetryNoopTest struct {
	integrations.Test
	cfg       processRetryChildConfig
	startTime time.Time
	writer    *processRetryResultWriter
}

func newProcessRetryNoopTest(cfg processRetryChildConfig, startTime time.Time, writer *processRetryResultWriter) integrations.Test {
	return &processRetryNoopTest{
		Test:      integrations.NewProcessRetryNoopTest(cfg.TestName, startTime),
		cfg:       cfg,
		startTime: startTime,
		writer:    writer,
	}
}

func (t *processRetryNoopTest) writePanicResult(info *processRetryErrorInfo) {
	if t == nil || t.writer == nil || info == nil {
		return
	}
	finish := time.Now()
	t.writer.Write(processRetryResult{
		Version:        1,
		TestName:       t.cfg.TestName,
		Attempt:        t.cfg.Attempt,
		RetryReason:    t.cfg.RetryReason,
		Status:         processRetryStatusFail,
		Failed:         true,
		Panic:          true,
		ErrorType:      info.Type,
		ErrorMessage:   info.Message,
		ErrorStack:     info.Stack,
		StartUnixNano:  t.startTime.UnixNano(),
		FinishUnixNano: finish.UnixNano(),
		DurationNanos:  finish.Sub(t.startTime).Nanoseconds(),
	})
}
