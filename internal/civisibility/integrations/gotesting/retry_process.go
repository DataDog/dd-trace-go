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
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/envconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/coverage"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

func processRetryModeEnabledFromEnv() bool {
	raw := strings.ToLower(strings.TrimSpace(env.Get(constants.CIVisibilityRetryExecutionModeEnvironmentVariable)))
	switch raw {
	case "", "in_process":
		return false
	case "process":
		return true
	default:
		log.Debug("civisibility: unsupported retry execution mode, using in_process")
		return false
	}
}

func processRetryConfiguredMaxConcurrencyFromEnv() (int, bool) {
	raw := strings.TrimSpace(env.Get(constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		log.Debug("civisibility: unsupported retry process max concurrency, using the effective default")
		return 0, false
	}
	return n, true
}

func processRetryDefaultMaxConcurrencyForCPU(currentCPU int) int {
	if internal.BoolEnv(constants.CIVisibilityInternalParallelEarlyFlakeDetectionEnabled, false) {
		return min(max(currentCPU, 1), int(internalParallelEFDMaxConcurrency))
	}
	return 1
}

func processRetryMaxConcurrencyForBaseline(baseline *processRetryLaunchBaseline, currentCPU int) int {
	if baseline != nil && baseline.maxConcurrencySet {
		return baseline.maxConcurrency
	}
	return processRetryDefaultMaxConcurrencyForCPU(currentCPU)
}

func processRetryParallelMaxConcurrencyForBaseline(baseline *processRetryLaunchBaseline) int64 {
	currentCPU := processRetryCurrentCPU()
	if baseline != nil && baseline.currentCPU > 0 {
		currentCPU = baseline.currentCPU
	}
	return int64(processRetryMaxConcurrencyForBaseline(baseline, currentCPU))
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
	ResultPath             string
	TestName               string
	Attempt                int
	RetryReason            string
	MRunEpoch              uint64
	InvocationOrdinal      uint64
	ParentDeadlineUnixNano int64
	ParentDeadlineOK       bool
	ObservedGOMAXPROCS     int
	Subtree                *processRetrySubtreeConfig
	controlConfig          processRetryControlConfig
	controlConfigLoaded    bool
}

type processRetryStatus string

const (
	processRetryStatusPass                            processRetryStatus = "pass"
	processRetryStatusFail                            processRetryStatus = "fail"
	processRetryStatusSkip                            processRetryStatus = "skip"
	processRetryStatusNotRun                          processRetryStatus = "not_run"
	processRetryStatusControlledPanicReady            processRetryStatus = "controlled_panic_ready"
	processRetryStatusControlledUnexpectedGoexitReady processRetryStatus = "controlled_unexpected_goexit_ready"

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
	processRetryFailureExitCode            = 1
	processRetryControlledPanicExitCode    = 2
	processRetryOutputDrainWait            = 1 * time.Second
	processRetryOutputDrainBudget          = processRetryOutputDrainWait
	processRetryKillGracePeriod            = 2 * time.Second
	processRetryPostKillWait               = 2 * time.Second
	processRetryParentDeadlineSafetyMargin = 500 * time.Millisecond
	processRetryShutdownWait               = processRetryKillGracePeriod + processRetryPostKillWait + processRetryOutputDrainBudget + processRetryParentDeadlineSafetyMargin
	processRetryDefaultTimeout             = 10 * time.Minute
)

type processRetryResult struct {
	Version           int                                `json:"version"`
	TestName          string                             `json:"test_name"`
	Attempt           int                                `json:"attempt"`
	RetryReason       string                             `json:"retry_reason"`
	MRunEpoch         uint64                             `json:"m_run_epoch,omitempty"`
	InvocationOrdinal uint64                             `json:"invocation_ordinal,omitempty"`
	Status            processRetryStatus                 `json:"status"`
	StartUnixNano     int64                              `json:"start_unix_nano"`
	FinishUnixNano    int64                              `json:"finish_unix_nano"`
	DurationNanos     int64                              `json:"duration_nanos"`
	Failed            bool                               `json:"failed"`
	Skipped           bool                               `json:"skipped"`
	Panic             bool                               `json:"panic"`
	RaceDetected      bool                               `json:"race_detected,omitempty"`
	RootParallel      bool                               `json:"root_parallel,omitempty"`
	ErrorType         string                             `json:"error_type,omitempty"`
	ErrorMessage      string                             `json:"error_message,omitempty"`
	ErrorStack        string                             `json:"error_stack,omitempty"`
	SkipReason        string                             `json:"skip_reason,omitempty"`
	ResultError       string                             `json:"result_error,omitempty"`
	OutputTail        string                             `json:"output_tail,omitempty"`
	OutputTruncated   bool                               `json:"output_truncated,omitempty"`
	Source            *processRetryTestSource            `json:"source,omitempty"`
	Coverage          []coverage.ProcessTestCoverageFile `json:"coverage,omitempty"`
	Subtests          []processRetrySubtreeResult        `json:"subtests,omitempty"`
	SkippedByITR      bool                               `json:"skipped_by_itr,omitempty"`
	ITRForcedRun      bool                               `json:"itr_forced_run,omitempty"`
	Modified          bool                               `json:"modified,omitempty"`
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
	errProcessRetryMultipleMRun        = errors.New("process retry child invoked testing.M.Run more than once")
)

var lookupProcessRetryChildTransport = integrations.LookupProcessRetryChildTransport
var newProcessRetryChildControl = newChildProcessRetryControl

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
	if wire, readErr := readProcessRetryControlConfig(processRetryControlConfigPath(cfg.ResultPath), cfg); readErr == nil {
		cfg = enrichProcessRetryChildConfig(cfg, wire)
		cfg.controlConfig = wire
		cfg.controlConfigLoaded = true
	} else if cfg.RetryReason == processRetrySubtreeReason {
		return cfg, readErr
	}
	if cfg.RetryReason == processRetrySubtreeReason && cfg.Subtree == nil {
		return cfg, errProcessRetryControlInvalid
	}
	if integrations.ProcessRetryChildTransportError() != nil {
		return cfg, errors.New("retry child transport cleanup failed")
	}
	return cfg, nil
}

func enrichProcessRetryChildConfig(cfg processRetryChildConfig, wire processRetryControlConfig) processRetryChildConfig {
	cfg.MRunEpoch = wire.MRunEpoch
	cfg.InvocationOrdinal = wire.InvocationOrdinal
	cfg.ParentDeadlineUnixNano = wire.ParentDeadlineUnixNano
	cfg.ParentDeadlineOK = wire.ParentDeadlineOK
	cfg.ObservedGOMAXPROCS = wire.ObservedGOMAXPROCS
	cfg.Subtree = wire.Subtree
	return cfg
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

type processRetryFuzzGuardSnapshot struct {
	once     sync.Once
	evaluate func() bool
	active   bool
}

func (s *processRetryFuzzGuardSnapshot) resolve() (bool, bool) {
	if s == nil || s.evaluate == nil {
		return false, false
	}
	s.once.Do(func() {
		s.active = s.evaluate()
	})
	return s.active, true
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

// Go propagates GOCOVERDIR to subprocesses; retry children must not merge their counters into the parent's profile.
const processRetryCoverageDirectoryEnvironmentVariable = "GOCOVERDIR"

func sanitizeProcessRetryBaseEnv(base []string) []string {
	result := make([]string, 0, len(base))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			result = append(result, entry)
			continue
		}
		if isProcessRetryInternalEnvKey(key) || strings.EqualFold(key, processRetryCoverageDirectoryEnvironmentVariable) {
			continue
		}
		if strings.EqualFold(key, constants.CIVisibilityEnabledEnvironmentVariable) {
			if mode, valid := envconfig.ParseEnabledMode(value); valid && mode == envconfig.EnabledModeParent {
				result = append(result, key+"=false")
				continue
			}
		}
		result = append(result, entry)
	}
	return result
}

func buildProcessRetryEnv(base []string, cfg processRetryChildConfig) []string {
	result := make([]string, 0, len(base)+5)
	result = append(result, base...)
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
	start     int
	truncated bool
}

func newProcessRetryBoundedOutput(maxBytes int64) *processRetryBoundedOutput {
	if maxBytes < 0 {
		maxBytes = 0
	} else if maxBytes > processRetryStreamMaxBytes {
		maxBytes = processRetryStreamMaxBytes
	}
	return &processRetryBoundedOutput{
		maxBytes: maxBytes,
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
		limit := int(w.maxBytes)
		if cap(w.tail) < limit {
			w.tail = make([]byte, limit)
		} else {
			w.tail = w.tail[:limit]
		}
		copy(w.tail, p[len(p)-limit:])
		w.start = 0
		w.truncated = true
		return
	}
	space := int(w.maxBytes) - len(w.tail)
	if len(p) <= space {
		w.appendGrowingTailLocked(p)
		return
	}
	if space > 0 {
		w.appendGrowingTailLocked(p[:space])
		p = p[space:]
	}
	if len(p) > 0 {
		written := copy(w.tail[w.start:], p)
		copy(w.tail, p[written:])
		w.start = (w.start + len(p)) % len(w.tail)
		w.truncated = true
	}
}

func (w *processRetryBoundedOutput) appendGrowingTailLocked(p []byte) {
	oldLen := len(w.tail)
	maxBytes := int(w.maxBytes)
	if remaining := maxBytes - oldLen; len(p) > remaining {
		p = p[:remaining]
	}
	if len(p) > cap(w.tail)-oldLen {
		newCap := maxBytes
		for _, candidate := range [...]int{64, 256, 1024, 4096, 16 * 1024} {
			if candidate > maxBytes {
				break
			}
			if len(p) <= candidate-oldLen {
				newCap = candidate
				break
			}
		}
		grown := make([]byte, oldLen, newCap)
		copy(grown, w.tail)
		w.tail = grown
	}
	w.tail = append(w.tail, p...)
}

func (w *processRetryBoundedOutput) Tail() (string, bool) {
	if w == nil {
		return "", false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	truncated := w.truncated || w.total > int64(len(w.tail))
	if len(w.tail) == 0 || w.start == 0 {
		return string(w.tail), truncated
	}
	var tail strings.Builder
	tail.Grow(len(w.tail))
	_, _ = tail.Write(w.tail[w.start:])
	_, _ = tail.Write(w.tail[:w.start])
	return tail.String(), truncated
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

type processRetryOutputWaiter interface {
	FinishAfterWait(time.Duration) error
}

func finishProcessRetryOutputCapturesAfterWait(timeout time.Duration, captures ...processRetryOutputWaiter) error {
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
	separator := stdoutTail != "" && stderrTail != ""
	truncated := stdoutTruncated || stderrTruncated
	combinedBytes := len(stdoutTail) + len(stderrTail)
	if separator {
		combinedBytes++
	}
	if maxBytes >= 0 && int64(combinedBytes) > maxBytes {
		truncated = true
		budget := int(maxBytes)
		switch {
		case budget == 0:
			stdoutTail = ""
			stderrTail = ""
			separator = false
		case stderrTail == "":
			stdoutTail = stdoutTail[len(stdoutTail)-budget:]
		case len(stderrTail) >= budget:
			stderrTail = stderrTail[len(stderrTail)-budget:]
			stdoutTail = ""
			separator = false
		default:
			budget -= len(stderrTail) + 1
			if len(stdoutTail) > budget {
				stdoutTail = stdoutTail[len(stdoutTail)-budget:]
			}
		}
	}

	if !truncated && !separator {
		if stdoutTail != "" {
			return stdoutTail, false, errors.Join(stdoutErr, stderrErr)
		}
		return stderrTail, false, errors.Join(stdoutErr, stderrErr)
	}
	combinedBytes = len(stdoutTail) + len(stderrTail)
	if separator {
		combinedBytes++
	}
	if truncated {
		combinedBytes += len(processRetryOutputTruncationMarker)
	}
	var combined strings.Builder
	combined.Grow(combinedBytes)
	if truncated {
		combined.WriteString(processRetryOutputTruncationMarker)
	}
	combined.WriteString(stdoutTail)
	if separator {
		combined.WriteByte('\n')
	}
	combined.WriteString(stderrTail)
	return combined.String(), truncated, errors.Join(stdoutErr, stderrErr)
}

type processRetryAttemptResult struct {
	Result                      processRetryResult
	TempDir                     string
	OutputTail                  string
	OutputTruncated             bool
	ExitCode                    int
	ExitStatusObserved          bool
	StartTime                   time.Time
	FinishTime                  time.Time
	Err                         error
	CaptureErr                  error
	TimedOut                    bool
	Unreaped                    bool
	ContainmentLost             bool
	SetupFailure                bool
	BodyAdmitted                bool
	ControlledTerminalCommitted bool
	Cleanup                     func()
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
	efdFellBackToFlakyRetries     bool
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
	hooks             processRetryRunnerHooks
	executable        string
	workingDirectory  string
	args              []string
	argsSnapshot      processRetryArgsSnapshot
	environment       []string
	currentCPU        int
	maxConcurrency    int
	maxConcurrencySet bool
	timeout           time.Duration
	timeoutSet        bool
	err               error
}

type processRetryStartupSnapshot struct {
	workingDirectory string
	args             []string
	environment      []string
	err              error
}

type processRetryArgsSnapshot struct {
	captured         bool
	preserved        []string
	boundary         []string
	runSelector      string
	skipSelector     string
	artifactOutput   string
	artifactsEnabled bool
	timeout          time.Duration
	timeoutSet       bool
	ok               bool
	reason           string
}

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
	controlEnabled   bool
}

type processRetryRealTimer struct {
	timer *time.Timer
}

func (t *processRetryRealTimer) C() <-chan time.Time { return t.timer.C }
func (t *processRetryRealTimer) Stop() bool          { return t.timer.Stop() }

type processRetryLimiter struct {
	mu         locking.Mutex
	active     int
	waiterHead *processRetryLimiterWaiter
	waiterTail *processRetryLimiterWaiter
}

type processRetryLimiterWaiter struct {
	maxConcurrency int
	ready          chan struct{}
	next           *processRetryLimiterWaiter
	granted        bool
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
	shuttingDown   atomic.Bool
	shutdown       chan struct{}
	changed        chan struct{}
	waiters        int
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
var processRetryStartup = captureProcessRetryStartupSnapshot(
	os.Getwd,
	func() []string { return os.Args[1:] },
	os.Environ,
)
var processRetryLaunchGate = processRetryLaunchGateState{
	shutdown: make(chan struct{}),
	changed:  make(chan struct{}),
}
var processRetryActiveChildren = struct {
	mu                     locking.Mutex
	children               map[*exec.Cmd]processRetryActiveChild
	closeActionRegistered  atomic.Bool
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
			return startAndWaitProcessRetryChild(cmd, retainProcessTreeHandle)
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
		controlEnabled:  true,
	}
}

func startAndWaitProcessRetryChild(
	cmd *exec.Cmd,
	retain func(*exec.Cmd) error,
) (<-chan error, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	retainErr := retain(cmd)
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()
	return waitCh, retainErr
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

func captureProcessRetryLaunchTemplate() *processRetryLaunchBaseline {
	return captureProcessRetryLaunchTemplateFromStartup(processRetryStartup)
}

func captureProcessRetryStartupSnapshot(
	workingDirectory func() (string, error),
	args func() []string,
	environ func() []string,
) processRetryStartupSnapshot {
	dir, err := workingDirectory()
	return processRetryStartupSnapshot{
		workingDirectory: dir,
		args:             append([]string(nil), args()...),
		environment:      sanitizeProcessRetryBaseEnv(environ()),
		err:              err,
	}
}

func captureProcessRetryLaunchTemplateFromStartup(startup processRetryStartupSnapshot) *processRetryLaunchBaseline {
	hooks := currentProcessRetryRunnerHooks()
	baseline := &processRetryLaunchBaseline{
		hooks:            hooks,
		workingDirectory: startup.workingDirectory,
		args:             append([]string(nil), startup.args...),
		environment:      append([]string(nil), startup.environment...),
		err:              startup.err,
	}
	if baseline.err != nil {
		return baseline
	}
	baseline.executable, baseline.err = hooks.executable()
	if baseline.err != nil {
		return baseline
	}
	baseline.argsSnapshot = captureProcessRetryArgsSnapshot(baseline.args)
	baseline.timeout, baseline.timeoutSet = processRetryTimeoutFromEnv()
	baseline.maxConcurrency, baseline.maxConcurrencySet = processRetryConfiguredMaxConcurrencyFromEnv()
	return baseline
}

func captureProcessRetryLaunchBaseline() *processRetryLaunchBaseline {
	return captureProcessRetryLaunchBaselineFromTemplate(nil)
}

func captureProcessRetryLaunchBaselineFromTemplate(template *processRetryLaunchBaseline) *processRetryLaunchBaseline {
	if template == nil {
		template = captureProcessRetryLaunchTemplate()
	}
	baseline := *template
	if baseline.err != nil {
		return &baseline
	}
	baseline.currentCPU = processRetryCurrentCPU()
	return &baseline
}

func processRetryLaunchesDisabled() bool {
	return processRetryLaunchGate.disabled.Load()
}

func processRetryShuttingDown() bool {
	return processRetryLaunchGate.shuttingDown.Load()
}

type processRetryGroupLease struct {
	shutdown <-chan struct{}
	released atomic.Bool
}

func acquireProcessRetryGroupLease() (*processRetryGroupLease, error) {
	if !processRetryShutdownActionRegistered() {
		return nil, errProcessRetryShutdown
	}
	processRetryLaunchGate.mu.Lock()
	defer processRetryLaunchGate.mu.Unlock()
	processRetryLaunchGate.ensureChannelsLocked()
	if processRetryLaunchGate.shuttingDown.Load() {
		return nil, errProcessRetryShutdown
	}
	if processRetryLaunchGate.disabled.Load() {
		return nil, errProcessRetryLaunchDisabled
	}
	processRetryLaunchGate.activeGroups++
	return &processRetryGroupLease{shutdown: processRetryLaunchGate.shutdown}, nil
}

func (l *processRetryGroupLease) release() {
	if l == nil || !l.released.CompareAndSwap(false, true) {
		return
	}
	processRetryLaunchGate.mu.Lock()
	processRetryLaunchGate.activeGroups--
	processRetryLaunchGate.notifyLocked()
	processRetryLaunchGate.mu.Unlock()
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
		changed := processRetryLaunchGate.beginWaitLocked()
		processRetryLaunchGate.mu.Unlock()
		timedOut := false
		select {
		case <-changed:
		case <-timer.C:
			timedOut = true
		}
		processRetryLaunchGate.endWait()
		if timedOut {
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
	if !g.shuttingDown.Load() {
		g.shuttingDown.Store(true)
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
	return processRetryActiveChildren.closeActionRegistered.Load()
}

func registerProcessRetryShutdownAction() bool {
	for {
		processRetryActiveChildren.mu.Lock()
		if processRetryActiveChildren.closeActionRegistered.Load() {
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
		processRetryActiveChildren.closeActionRegistered.Store(registered)
		processRetryActiveChildren.closeActionRegistering = false
		close(changed)
		processRetryActiveChildren.closeActionChanged = nil
		processRetryActiveChildren.mu.Unlock()
		return registered
	}
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
	coordinators := snapshotProcessRetryCoordinators()
	for _, coordinator := range coordinators {
		coordinator.requestShutdown()
	}
	processRetryActiveChildren.mu.Lock()
	children := make([]processRetryActiveChild, 0, len(processRetryActiveChildren.children))
	for cmd, child := range processRetryActiveChildren.children {
		if !child.shutdownKillIssued {
			children = append(children, child)
			child.shutdownKillIssued = true
			processRetryActiveChildren.children[cmd] = child
		}
	}
	processRetryActiveChildren.closeActionRegistered.Store(false)
	processRetryActiveChildren.mu.Unlock()
	for _, child := range children {
		if err := errors.Join(child.killTree(child.cmd), child.killDirect(child.cmd)); err != nil {
			log.Debug("civisibility: failed to stop active process retry child: %v", err.Error())
		}
	}
	deadline := time.Now().Add(processRetryShutdownWait)
	for _, coordinator := range coordinators {
		coordinator.completeShutdown()
	}
	for _, coordinator := range coordinators {
		if !coordinator.awaitCompletion(deadline) {
			log.Debug("civisibility: timed out waiting for deferred process retry coordinator during shutdown")
			break
		}
	}
	remaining := max(time.Until(deadline), 0)
	if !waitForProcessRetryShutdownQuiescence(remaining) {
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
		if processRetryLaunchGate.shuttingDown.Load() {
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
			case processRetryLaunchGate.shuttingDown.Load():
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
		changed := processRetryLaunchGate.beginWaitLocked()
		processRetryLaunchGate.mu.Unlock()

		select {
		case <-changed:
			processRetryLaunchGate.endWait()
			continue
		case <-ctx.Done():
			processRetryLaunchGate.endWait()
			return nil, errors.Join(errProcessRetryLaunchCanceled, ctx.Err())
		case <-parentDeadlineHardCap:
			processRetryLaunchGate.endWait()
			if err := ctx.Err(); err != nil {
				return nil, errors.Join(errProcessRetryLaunchCanceled, err)
			}
			return nil, errors.Join(errProcessRetryLaunchDeadline, context.DeadlineExceeded)
		}
	}
}

func (g *processRetryLaunchGateState) beginWaitLocked() <-chan struct{} {
	g.ensureChannelsLocked()
	g.waiters++
	return g.changed
}

func (g *processRetryLaunchGateState) endWait() {
	g.mu.Lock()
	g.waiters--
	g.mu.Unlock()
}

func (g *processRetryLaunchGateState) notifyLocked() {
	if g.waiters == 0 {
		return
	}
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

func (l *processRetryLimiter) acquireWithShutdownLimit(
	ctx context.Context,
	parentDeadlineHardCap <-chan time.Time,
	shutdown <-chan struct{},
	maxConcurrency int,
) processRetryLimiterAcquireResult {
	if ctx == nil {
		ctx = context.Background()
	}
	maxConcurrency = max(maxConcurrency, 1)
	if processRetryShutdownRequested(shutdown) {
		return processRetryLimiterAcquireResult{Cause: processRetryLimiterShutdown, Err: errProcessRetryShutdown}
	}
	if err := ctx.Err(); err != nil {
		return processRetryLimiterAcquireResult{Cause: processRetryLimiterExternalCancel, Err: err}
	}

	acquired, waiter := l.tryAcquireOrQueue(maxConcurrency)
	if !acquired {
		select {
		case <-waiter.ready:
		case <-ctx.Done():
			l.cancelWaiter(waiter)
			return processRetryLimiterAcquireResult{Cause: processRetryLimiterExternalCancel, Err: ctx.Err()}
		case <-shutdown:
			l.cancelWaiter(waiter)
			return processRetryLimiterAcquireResult{Cause: processRetryLimiterShutdown, Err: errProcessRetryShutdown}
		case <-parentDeadlineHardCap:
			l.cancelWaiter(waiter)
			if err := ctx.Err(); err != nil {
				return processRetryLimiterAcquireResult{Cause: processRetryLimiterExternalCancel, Err: err}
			}
			return processRetryLimiterAcquireResult{Cause: processRetryLimiterParentDeadline, Err: context.DeadlineExceeded}
		}
	}

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(l.release)
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
}

func (l *processRetryLimiter) tryAcquireOrQueue(maxConcurrency int) (bool, *processRetryLimiterWaiter) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.waiterHead == nil && l.active < maxConcurrency {
		l.active++
		return true, nil
	}

	waiter := &processRetryLimiterWaiter{
		maxConcurrency: maxConcurrency,
		ready:          make(chan struct{}),
	}
	if l.waiterTail == nil {
		l.waiterHead = waiter
	} else {
		l.waiterTail.next = waiter
	}
	l.waiterTail = waiter
	l.grantWaitersLocked()
	return waiter.granted, waiter
}

func (l *processRetryLimiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.releaseLocked()
}

func (l *processRetryLimiter) releaseLocked() {
	if l.active == 0 {
		return
	}
	l.active--
	l.grantWaitersLocked()
}

func (l *processRetryLimiter) cancelWaiter(waiter *processRetryLimiterWaiter) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if waiter.granted {
		l.releaseLocked()
		return
	}
	var previous *processRetryLimiterWaiter
	for current := l.waiterHead; current != nil; current = current.next {
		if current == waiter {
			l.removeWaiterLocked(previous, current)
			return
		}
		previous = current
	}
}

func (l *processRetryLimiter) grantWaitersLocked() {
	for {
		var previous *processRetryLimiterWaiter
		waiter := l.waiterHead
		for waiter != nil && l.active >= waiter.maxConcurrency {
			previous = waiter
			waiter = waiter.next
		}
		if waiter == nil {
			return
		}

		l.removeWaiterLocked(previous, waiter)
		waiter.granted = true
		l.active++
		close(waiter.ready)
	}
}

func (l *processRetryLimiter) removeWaiterLocked(previous, waiter *processRetryLimiterWaiter) {
	if previous == nil {
		l.waiterHead = waiter.next
	} else {
		previous.next = waiter.next
	}
	if l.waiterTail == waiter {
		l.waiterTail = previous
	}
	waiter.next = nil
}

func processRetryShutdownRequested(shutdown <-chan struct{}) bool {
	select {
	case <-shutdown:
		return true
	default:
		return false
	}
}

func processRetryParentDeadlineReserve() time.Duration {
	return processRetryKillGracePeriod + processRetryPostKillWait + processRetryOutputDrainBudget + processRetryParentDeadlineSafetyMargin
}

func runProcessRetryAttemptWithBaselineAndShutdown(
	ctx context.Context,
	cfg processRetryChildConfig,
	parentDeadline time.Time,
	parentDeadlineOK bool,
	baseline *processRetryLaunchBaseline,
	shutdown <-chan struct{},
	parentParallelBridge func() error,
) processRetryAttemptResult {
	if ctx == nil {
		ctx = context.Background()
	}
	parentStart := time.Now()
	attempt := processRetryAttemptResult{
		ExitCode:  processRetryExitCodeUnset,
		StartTime: parentStart,
	}
	finishSetupFailure := func(err error, timedOut bool) processRetryAttemptResult {
		attempt.SetupFailure = true
		attempt.TimedOut = timedOut
		attempt.Err = err
		attempt.FinishTime = time.Now()
		return attempt
	}
	if processRetryShutdownRequested(shutdown) || processRetryShuttingDown() {
		return finishSetupFailure(errProcessRetryShutdown, false)
	}
	if processRetryLaunchesDisabled() {
		return finishSetupFailure(errProcessRetryLaunchDisabled, false)
	}
	if baseline == nil {
		baseline = captureProcessRetryLaunchBaseline()
	}
	if baseline.err != nil {
		return finishSetupFailure(baseline.err, false)
	}
	hooks := resolveProcessRetryRunnerHooks(baseline.hooks)
	executable := baseline.executable
	workingDir := baseline.workingDirectory
	argsSnapshot := baseline.argsSnapshot
	if !argsSnapshot.captured {
		argsSnapshot = captureProcessRetryArgsSnapshot(baseline.args)
	}
	if !argsSnapshot.ok {
		return finishSetupFailure(errors.New(argsSnapshot.reason), false)
	}
	currentCPU := cfg.ObservedGOMAXPROCS
	if currentCPU < 1 {
		currentCPU = baseline.currentCPU
	}
	if currentCPU < 1 {
		currentCPU = processRetryCurrentCPU()
	}
	selectedTimeout := selectedProcessRetryTimeout(argsSnapshot.timeout, argsSnapshot.timeoutSet, baseline.timeout, baseline.timeoutSet, parentDeadline, parentDeadlineOK, hooks.now())
	if selectedTimeout <= 0 {
		return finishSetupFailure(context.DeadlineExceeded, true)
	}
	if err := ctx.Err(); err != nil {
		return finishSetupFailure(err, false)
	}
	var parentDeadlineHardCap <-chan time.Time
	var parentDeadlineTimer processRetryTimer
	if parentDeadlineOK {
		parentDeadlineRemaining := parentDeadline.Sub(hooks.now()) - processRetryParentDeadlineReserve()
		if parentDeadlineRemaining <= 0 {
			return finishSetupFailure(context.DeadlineExceeded, true)
		}
		parentDeadlineTimer = hooks.newTimer(parentDeadlineRemaining)
		parentDeadlineHardCap = parentDeadlineTimer.C()
		defer parentDeadlineTimer.Stop()
	}
	limiterResult := getProcessRetryLimiter().acquireWithShutdownLimit(
		ctx,
		parentDeadlineHardCap,
		shutdown,
		processRetryMaxConcurrencyForBaseline(baseline, currentCPU),
	)
	if limiterResult.Cause != processRetryLimiterAcquired {
		return finishSetupFailure(limiterResult.Err, limiterResult.Cause == processRetryLimiterParentDeadline)
	}
	defer limiterResult.Release()
	if processRetryShutdownRequested(shutdown) || processRetryShuttingDown() {
		return finishSetupFailure(errProcessRetryShutdown, false)
	}
	if processRetryLaunchesDisabled() {
		return finishSetupFailure(errProcessRetryLaunchDisabled, false)
	}
	if err := ctx.Err(); err != nil {
		return finishSetupFailure(err, false)
	}
	selectedTimeout = selectedProcessRetryTimeout(argsSnapshot.timeout, argsSnapshot.timeoutSet, baseline.timeout, baseline.timeoutSet, parentDeadline, parentDeadlineOK, hooks.now())
	if selectedTimeout <= 0 {
		return finishSetupFailure(context.DeadlineExceeded, true)
	}
	if parentDeadlineOK {
		parentRemaining := parentDeadline.Sub(hooks.now()) - processRetryParentDeadlineReserve()
		if parentRemaining < selectedTimeout {
			selectedTimeout = parentRemaining
		}
		if selectedTimeout <= 0 {
			return finishSetupFailure(context.DeadlineExceeded, true)
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
		return finishSetupFailure(err, false)
	}
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
	childCfg.ObservedGOMAXPROCS = currentCPU
	childCfg.ParentDeadlineOK = parentDeadlineOK
	if parentDeadlineOK {
		childCfg.ParentDeadlineUnixNano = parentDeadline.UnixNano()
	}
	stdoutCapture, err := newProcessRetryOutputCapture(processRetryStreamMaxBytes)
	if err != nil {
		return finishSetupFailure(err, false)
	}
	stderrCapture, err := newProcessRetryOutputCapture(processRetryStreamMaxBytes)
	if err != nil {
		_ = stdoutCapture.CloseSetupFailure()
		return finishSetupFailure(err, false)
	}
	closeCapturesForSetupFailure := func() {
		_ = stdoutCapture.CloseSetupFailure()
		_ = stderrCapture.CloseSetupFailure()
	}
	if err := ctx.Err(); err != nil {
		closeCapturesForSetupFailure()
		return finishSetupFailure(err, false)
	}
	selectedTimeout = remainingAttemptTime()
	if selectedTimeout <= 0 || attemptDeadlineReached() {
		closeCapturesForSetupFailure()
		return finishSetupFailure(context.DeadlineExceeded, true)
	}

	cmd := hooks.command(executable)
	cmd.Env = buildProcessRetryEnv(baseline.environment, childCfg)
	cmd.Dir = workingDir
	cmd.Stdin = nil
	cmd.Stdout = stdoutCapture.ChildWriter()
	cmd.Stderr = stderrCapture.ChildWriter()
	var control *processRetryControl
	if hooks.controlEnabled {
		control, err = newParentProcessRetryControl(cmd, childCfg)
		if err != nil {
			closeCapturesForSetupFailure()
			return finishSetupFailure(err, false)
		}
		defer control.Close()
	}
	if err := hooks.prepareTree(cmd); err != nil {
		closeCapturesForSetupFailure()
		return finishSetupFailure(err, false)
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
		return finishSetupFailure(errors.Join(err, releaseTree()), false)
	}
	latestTimeout := remainingAttemptTime()
	if latestTimeout <= 0 || attemptDeadlineReached() {
		closeCapturesForSetupFailure()
		return finishSetupFailure(errors.Join(context.DeadlineExceeded, releaseTree()), true)
	}
	selectedTimeout = latestTimeout
	childTestingTimeout := selectedTimeout + processRetryParentDeadlineReserve()
	filteredArgs, ok, reason := buildProcessRetryArgsFromSnapshot(argsSnapshot, cfg.TestName, currentCPU, childTestingTimeout)
	if !ok {
		closeCapturesForSetupFailure()
		return finishSetupFailure(errors.Join(errors.New(reason), releaseTree()), false)
	}
	cmd.Args = append([]string{executable}, filteredArgs...)

	stdoutCapture.StartCopy()
	stderrCapture.StartCopy()
	waitCh, startErr := startProcessRetryChild(ctx, attemptTimer.C(), hooks, cmd)
	if control != nil {
		attempt.Err = errors.Join(attempt.Err, control.CloseChildEndpoints())
	}
	if startErr != nil && waitCh == nil {
		closeCapturesForSetupFailure()
		timedOut := errors.Is(startErr, errProcessRetryLaunchDeadline)
		return finishSetupFailure(errors.Join(startErr, releaseTree()), timedOut)
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
	var controlErrors <-chan error
	if startErr != nil {
		attempt.SetupFailure = true
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
				nil,
				attemptTimer,
				&attempt,
				teardownPhase,
				markContainmentLost,
			)
		}
	} else if attachErr := hooks.attachTree(cmd); attachErr != nil {
		attempt.SetupFailure = true
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
		attempt.Err = errors.Join(attempt.Err, resumeErr)
		waitErr = forceKillAndWait(hooks.killTree)
	} else {
		if control != nil {
			var childExited bool
			var observedWaitErr error
			attempt.BodyAdmitted, childExited, observedWaitErr, err = control.parentAdmission(ctx, shutdown, attemptTimer.C(), waitCh)
			if childExited {
				teardownPhase.begin()
				waitErr = observedWaitErr
			} else if err != nil {
				attempt.SetupFailure = true
				attempt.TimedOut = errors.Is(err, context.DeadlineExceeded)
				attempt.Err = errors.Join(attempt.Err, errProcessRetryControlInvalid, err)
				waitErr = forceKillAndWait(hooks.killTree)
			} else {
				control.parallelBridge = parentParallelBridge
				controlErrors = control.serveParent()
			}
		}
		if waitErr != nil || (control != nil && !attempt.BodyAdmitted) {
			// Admission failure or a pre-ready child exit already owns wait/teardown.
		} else if attemptDeadlineReached() {
			attempt.TimedOut = true
			waitErr = forceKillAndWait(hooks.killTree)
		} else {
			waitErr = waitProcessRetryChildWithTeardown(ctx, shutdown, hooks, cmd, waitCh, controlErrors, attemptTimer, &attempt, teardownPhase, markContainmentLost)
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
	result, timingOK, resultErr := readProcessRetryResult(resultPath, childCfg)
	if resultErr != nil {
		attempt.Err = errors.Join(attempt.Err, resultErr)
	} else {
		attempt.Result = result
		if isProcessRetryControlledTerminalStatus(result.Status) && control != nil {
			terminal, terminalTimedOut, terminalErr := control.controlledTerminalState(ctx, shutdown, attemptTimer.C())
			applyProcessRetryControlledTerminalState(&attempt, terminal, terminalTimedOut, terminalErr)
		}
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

func applyProcessRetryControlledTerminalState(
	attempt *processRetryAttemptResult,
	terminal processRetryControlledTerminalState,
	timedOut bool,
	err error,
) {
	if attempt == nil {
		return
	}
	attempt.TimedOut = attempt.TimedOut || timedOut
	if err != nil {
		attempt.Err = errors.Join(attempt.Err, errProcessRetryControlInvalid, err)
	}
	attempt.ControlledTerminalCommitted = terminal.committed && terminal.status == attempt.Result.Status
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

func waitProcessRetryChildWithTeardown(
	ctx context.Context,
	shutdown <-chan struct{},
	hooks processRetryRunnerHooks,
	cmd *exec.Cmd,
	waitCh <-chan error,
	controlErrors <-chan error,
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
	for {
		select {
		case err := <-waitCh:
			return observeWaitResult(err)
		case err, ok := <-controlErrors:
			if !ok {
				controlErrors = nil
				continue
			}
			if waitErr, ok := drainWaitCh(); ok {
				return waitErr
			}
			attempt.Err = errors.Join(attempt.Err, errProcessRetryControlInvalid, err)
			return errors.Join(errProcessRetryControlInvalid, terminateAndWait())
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
	if errors.Is(attempt.Err, errProcessRetryMultipleMRun) {
		return failed("testmain_multiple_m_run")
	}
	if errors.Is(attempt.Err, errProcessRetryControlInvalid) {
		return failed("process_protocol_failure")
	}
	if attempt.SetupFailure {
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
	if attempt.Result.Panic && attempt.ExitStatusObserved && attempt.ExitCode != processRetryControlledPanicExitCode {
		return failed("testmain_exit_conflict")
	}
	if isProcessRetryControlledTerminalStatus(attempt.Result.Status) {
		if !attempt.ControlledTerminalCommitted {
			return failed("controlled_terminal_uncommitted")
		}
		return failed("test_panic")
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
		} else if attempt.Result.RaceDetected {
			kind = "test_race"
		}
		return failed(kind)
	default:
		return failed("missing_or_not_run")
	}
}

func processRetryFailureStopsContinuation(kind string) bool {
	switch kind {
	case "", "test_fail", "test_panic":
		return false
	default:
		return true
	}
}

func isProcessRetryControlledTerminalStatus(status processRetryStatus) bool {
	return status == processRetryStatusControlledPanicReady || status == processRetryStatusControlledUnexpectedGoexitReady
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
		efdFellBackToFlakyRetries:     execMeta.efdFellBackToFlakyRetries,
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
	execMeta.efdFellBackToFlakyRetries = snapshot.efdFellBackToFlakyRetries
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

func ensureProcessRetryInvocationOrdinal(options *runTestWithRetryOptions) uint64 {
	if options == nil {
		return 0
	}
	if options.processRetryInvocationOrdinal == 0 && options.processRetryInvocationCounter != nil {
		options.processRetryInvocationOrdinal = options.processRetryInvocationCounter.Add(1)
	}
	return options.processRetryInvocationOrdinal
}

func processRetryParallelBaselineReady(baseline *processRetryLaunchBaseline) (bool, string) {
	if !ProcessRetryContainmentSupported() {
		return false, errProcessRetryTreeUnsupported.Error()
	}
	if baseline == nil {
		return false, "missing_launch_baseline"
	}
	if baseline.err != nil {
		return false, baseline.err.Error()
	}
	argsSnapshot := baseline.argsSnapshot
	if !argsSnapshot.captured {
		argsSnapshot = captureProcessRetryArgsSnapshot(baseline.args)
	}
	if !argsSnapshot.ok {
		if argsSnapshot.reason == "" {
			return false, "invalid_args_snapshot"
		}
		return false, argsSnapshot.reason
	}
	return true, ""
}

func deferProcessRetryTestEventWithAdmission(
	testInfo *commonInfo,
	execMeta *testExecutionMetadata,
	attempt processRetryAttemptResult,
	admitContinuation func(processRetryEffectiveStatus),
) (processRetryEffectiveStatus, *deferredProcessRetryEvent) {
	tail := &deferredProcessRetryEvent{}
	effective := finishProcessRetryTestEvent(testInfo, execMeta, attempt, admitContinuation, tail)
	if !tail.ready {
		return effective, nil
	}
	return effective, tail
}

func finishProcessRetryTestEvent(
	testInfo *commonInfo,
	execMeta *testExecutionMetadata,
	attempt processRetryAttemptResult,
	admitContinuation func(processRetryEffectiveStatus),
	deferred *deferredProcessRetryEvent,
) processRetryEffectiveStatus {
	if testInfo == nil {
		effective := processRetryEffectiveStatus{Status: processRetryStatusFail, Failed: true, FailureKind: "missing_test_info"}
		if admitContinuation != nil {
			admitContinuation(effective)
		}
		return effective
	}
	module := session.GetOrCreateModule(testInfo.moduleName)
	suite := module.GetOrCreateSuite(testInfo.suiteName)
	test := suite.CreateTest(testInfo.testName, integrations.WithTestStartTime(attempt.StartTime))
	if testInfo.sourceFunc != nil {
		test.SetTestFunc(testInfo.sourceFunc)
	} else if source := attempt.Result.Source; source != nil {
		relativePath := utils.GetRelativePathFromCITagsSourceRoot(source.RuntimePath)
		test.SetTag(constants.TestSourceFile, relativePath)
		test.SetTag(constants.TestSourceStartLine, source.RuntimeStartLine)
		suite.SetTag(constants.TestSourceFile, relativePath)
		if source.RuntimeEndLine > 0 {
			test.SetTag(constants.TestSourceEndLine, source.RuntimeEndLine)
		}
		if codeOwners := utils.GetCodeOwners(); codeOwners != nil {
			if match, found := codeOwners.Match("/" + relativePath); found {
				test.SetTag(constants.TestCodeOwners, match.GetOwnersString())
				suite.SetTag(constants.TestCodeOwners, match.GetOwnersString())
			}
		}
		if source.Unskippable {
			test.SetTag(constants.TestUnskippable, "true")
			telemetry.ITRUnskippable(telemetry.TestEventType)
		}
	}
	execMeta.test = test
	coverage.SubmitProcessTestCoverage(session.SessionID(), suite.SuiteID(), test.TestID(), attempt.Result.Coverage)
	cancelExecution := setTestTagsFromExecutionMetadataNoClose(test, execMeta)
	// A validated process skip is already the outcome of the execution. In
	// particular, replaying an exact disabled descendant must not turn that skip
	// into the pre-execution metadata cancellation used by in-process tests.
	disabledSkip := execMeta.isDisabled && !execMeta.isAttemptToFix && attempt.Result.Status == processRetryStatusSkip
	cancelExecution = cancelExecution && !disabledSkip
	test.SetTag(constants.TestRetryExecutionMode, "process")
	if execMeta.isItrForcedRun {
		test.SetTag(constants.TestForcedToRun, "true")
		telemetry.ITRForcedRun(telemetry.TestEventType)
	}
	if execMeta.isItrSkipped {
		test.SetTag(constants.TestSkippedByITR, "true")
		telemetry.ITRSkipped(telemetry.TestEventType)
		currentITRState().markActualSkip()
		session.SetTag(constants.ITRTestsSkipped, "true")
		session.SetTag(constants.ITRTestsSkippingCount, numOfTestsSkipped.Add(1))
	}
	effective := effectiveProcessRetryStatus(attempt, cancelExecution)
	if admitContinuation != nil {
		admitContinuation(effective)
	}
	duration := max(attempt.FinishTime.Sub(attempt.StartTime), 0)
	finalExec := isFinalExecution(effective.Failed, effective.Skipped, execMeta, duration)
	if finalExec && deferred == nil {
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
		if execMeta.isAttemptToFix && execMeta.isARetry {
			attemptToFixPassed := effective.Status == processRetryStatusPass && execMeta.allAttemptsPassed
			test.SetTag(constants.TestAttemptToFixPassed, strconv.FormatBool(attemptToFixPassed))
		}
	}
	if effective.Failed {
		if finalExec && deferred == nil && execMeta.allRetriesFailed {
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
	if deferred != nil {
		deferred.event = test
		deferred.finishTime = attempt.FinishTime
		deferred.failed = effective.Failed
		deferred.ready = true
		switch {
		case effective.Failed:
			deferred.status = integrations.ResultStatusFail
		case effective.Skipped:
			deferred.status = integrations.ResultStatusSkip
			deferred.skipReason = attempt.Result.SkipReason
		default:
			deferred.status = integrations.ResultStatusPass
		}
		return effective
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

func captureProcessRetryArgsSnapshot(originalArgs []string) processRetryArgsSnapshot {
	preserved, boundary, runSelector, skipSelector, ok, reason := processRetryFilterArgs(originalArgs, true)
	timeout, timeoutSet := processRetryTimeoutFromArgs(originalArgs)
	artifactOutput, artifactsEnabled := processRetryArtifactPolicyFromArgs(originalArgs)
	return processRetryArgsSnapshot{
		captured:         true,
		preserved:        append([]string(nil), preserved...),
		boundary:         append([]string(nil), boundary...),
		runSelector:      runSelector,
		skipSelector:     skipSelector,
		artifactOutput:   artifactOutput,
		artifactsEnabled: artifactsEnabled,
		timeout:          timeout,
		timeoutSet:       timeoutSet,
		ok:               ok,
		reason:           reason,
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
	if snapshot.artifactsEnabled {
		if snapshot.artifactOutput != "" {
			inserted = append(inserted, "-test.outputdir="+snapshot.artifactOutput)
		}
		inserted = append(inserted, "-test.artifacts=true")
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

func processRetryArtifactPolicyFromArgs(originalArgs []string) (outputDir string, enabled bool) {
	for i := 0; i < len(originalArgs); i++ {
		arg := originalArgs[i]
		if arg == "--" || !processRetryIsFlagToken(arg) {
			break
		}
		name, value, hasValue := processRetrySplitFlag(arg)
		if arity, known := processRetryStripFlags[name]; known && arity == processRetryFlagValue && !hasValue && i+1 < len(originalArgs) {
			i++
			value = originalArgs[i]
		}
		switch name {
		case "-test.outputdir":
			outputDir = value
		case "-test.artifacts":
			enabled = true
			if hasValue {
				parsed, err := strconv.ParseBool(value)
				enabled = err == nil && parsed
			}
		}
	}
	return outputDir, enabled
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
			token := name + "=off"
			if !hasValue {
				if i+1 < len(originalArgs) {
					i++
				}
			}
			if buildArgs {
				preserved = append(preserved, token)
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

func processRetryReasonForExecution(execMeta *testExecutionMetadata) (string, bool) {
	if execMeta == nil {
		return "", false
	}
	if execMeta.isAttemptToFix {
		return constants.AttemptToFixRetryReason, true
	}
	if usesEfdRetrySemantics(execMeta) {
		return constants.EarlyFlakeDetectionRetryReason, true
	}
	if execMeta.isFlakyTestRetriesEnabled {
		return constants.AutoTestRetriesRetryReason, true
	}
	return "", false
}

func processRetryEligible(execMeta *testExecutionMetadata, options *runTestWithRetryOptions) (bool, string) {
	if isProcessRetryChild() {
		return false, "child_mode"
	}
	if options == nil {
		return false, "missing_options"
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
	retryReason, hasRetryReason := processRetryReasonForExecution(execMeta)
	if !hasRetryReason {
		return false, "flaky_retry_disabled"
	}
	if retryReason == constants.AttemptToFixRetryReason && !execMeta.shouldOrchestrateAttemptToFix {
		return false, "attempt_to_fix_not_owned"
	}
	if retryReason != constants.AttemptToFixRetryReason && execMeta.isQuarantined {
		return false, "quarantined"
	}
	if retryReason != constants.AttemptToFixRetryReason && execMeta.isDisabled {
		return false, "disabled"
	}
	fuzzActive, fuzzGuardSet := options.processRetryFuzzGuard.resolve()
	if !fuzzGuardSet {
		return false, "missing_fuzz_guard"
	}
	if fuzzActive {
		return false, "fuzz_active"
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
	proceed, finalize := instrumentProcessRetryChild(m, cfg)
	if !proceed {
		return finalize(processRetryFailureExitCode)
	}
	exitCode := m.Run()
	return finalize(exitCode)
}

type processRetryChildInstrumentationState struct {
	cfg          processRetryChildConfig
	control      *processRetryControl
	multipleMRun atomic.Bool
}

var processRetryChildInstrumentations = struct {
	mu     locking.Mutex
	states map[*testing.M]*processRetryChildInstrumentationState
}{states: make(map[*testing.M]*processRetryChildInstrumentationState)}

func instrumentProcessRetryChild(m *testing.M, cfg processRetryChildConfig) (bool, testingMFinalizer) {
	processRetryChildInstrumentations.mu.Lock()
	defer processRetryChildInstrumentations.mu.Unlock()
	if state := processRetryChildInstrumentations.states[m]; state != nil {
		state.multipleMRun.Store(true)
		if state.cfg != cfg {
			log.Debug("civisibility: conflicting process retry child instrumentation")
		}
		_ = state.control.Send(processRetryControlAbort, "testmain_multiple_m_run")
		clearProcessRetryChildWorkloads(
			getInternalTestArray(m),
			getInternalBenchmarkArray(m),
			getInternalFuzzTargetArray(m),
			getInternalExampleArray(m),
		)
		return false, failureTestingMFinalizer
	}

	writer := newProcessRetryResultWriter(cfg.ResultPath)
	control, err := newProcessRetryChildControl(cfg)
	if err != nil {
		writer.Write(processRetryNotRunResult(cfg, "control_protocol_failure"))
		clearProcessRetryChildWorkloads(
			getInternalTestArray(m),
			getInternalBenchmarkArray(m),
			getInternalFuzzTargetArray(m),
			getInternalExampleArray(m),
		)
		hardStopInvalidProcessRetryChild("control_protocol_failure")
		return false, failureTestingMFinalizer
	}
	cfg = control.cfg
	if err := control.childAdmission(); err != nil {
		_ = control.Close()
		writer.Write(processRetryNotRunResult(cfg, "control_protocol_failure"))
		clearProcessRetryChildWorkloads(
			getInternalTestArray(m),
			getInternalBenchmarkArray(m),
			getInternalFuzzTargetArray(m),
			getInternalExampleArray(m),
		)
		hardStopInvalidProcessRetryChild("control_protocol_failure")
		return false, failureTestingMFinalizer
	}
	if cfg.Subtree != nil && (cfg.Subtree.CollectPerTest || cfg.Subtree.CollectAggregate) {
		// The process child deliberately skips CI Visibility initialization,
		// so initialize only Go's runtime coverage hooks. Coverage is returned
		// in the validated result; the child never owns an upload transport.
		coverage.InitializeCoverage(m, false)
		if cfg.Subtree.CollectPerTest && !coverage.CanCollect() ||
			cfg.Subtree.CollectAggregate && !coverage.CanComputeCoverageProfile() {
			_ = control.Close()
			writer.Write(processRetryNotRunResult(cfg, "coverage_initialization_failed"))
			clearProcessRetryChildWorkloads(
				getInternalTestArray(m),
				getInternalBenchmarkArray(m),
				getInternalFuzzTargetArray(m),
				getInternalExampleArray(m),
			)
			hardStopInvalidProcessRetryChild("coverage_initialization_failed")
		}
	}
	releaseHookEpoch := activateTestingMHookEpoch(cfg.MRunEpoch)
	var finalizeOnce sync.Once
	finalize := func(exitCode int) int {
		finalizeOnce.Do(func() {
			writer.Write(processRetryNotRunResult(cfg, ""))
		})
		return exitCode
	}
	configuredFinalize := configureProcessRetryChildWorkloads(
		cfg,
		writer,
		control,
		finalize,
		getInternalTestArray(m),
		getInternalBenchmarkArray(m),
		getInternalFuzzTargetArray(m),
		getInternalExampleArray(m),
		hardStopInvalidProcessRetryChild,
	)
	state := &processRetryChildInstrumentationState{cfg: cfg, control: control}
	processRetryChildInstrumentations.states[m] = state
	return true, func(exitCode int) int {
		exitCode = configuredFinalize(exitCode)
		releaseHookEpoch()
		if state.multipleMRun.Load() {
			return processRetryFailureExitCode
		}
		return exitCode
	}
}

func configureProcessRetryChildWorkloads(
	cfg processRetryChildConfig,
	writer *processRetryResultWriter,
	control *processRetryControl,
	finalize testingMFinalizer,
	tests *[]testing.InternalTest,
	benchmarks *[]testing.InternalBenchmark,
	fuzzTargets *[]testing.InternalFuzzTarget,
	examples *[]testing.InternalExample,
	hardStop func(reason string),
) testingMFinalizer {
	if tests == nil || benchmarks == nil || fuzzTargets == nil || examples == nil {
		writer.Write(processRetryNotRunResult(cfg, "testing_m_reflection_drift"))
		clearProcessRetryChildWorkloads(tests, benchmarks, fuzzTargets, examples)
		hardStop("testing_m_reflection_drift")
		return finalize
	}

	var selected testing.InternalTest
	found := false
	topLevelName, _ := topLevelTestName(cfg.TestName)
	for _, test := range *tests {
		if test.Name == topLevelName {
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

	wrapped, finalizeSelected := wrapProcessRetryChildTest(selected.F, cfg, writer, control)
	selected.F = wrapped
	*tests = []testing.InternalTest{selected}
	return func(exitCode int) int {
		finalizeSelected()
		return finalize(exitCode)
	}
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

func (w *processRetryResultWriter) Write(result processRetryResult) bool {
	if w == nil {
		return false
	}
	written := false
	w.once.Do(func() {
		if strings.TrimSpace(w.path) == "" {
			return
		}
		if err := writeProcessRetryResultAtomically(w.path, result); err != nil {
			log.Debug("civisibility: process retry child failed to write result")
			return
		}
		written = true
	})
	return written
}

func processRetryNotRunResult(cfg processRetryChildConfig, resultError string) processRetryResult {
	return processRetryResult{
		Version:           1,
		TestName:          cfg.TestName,
		Attempt:           cfg.Attempt,
		RetryReason:       cfg.RetryReason,
		MRunEpoch:         cfg.MRunEpoch,
		InvocationOrdinal: cfg.InvocationOrdinal,
		Status:            processRetryStatusNotRun,
		ResultError:       resultError,
	}
}

type processRetryChildObservation struct {
	cfg          processRetryChildConfig
	writer       *processRetryResultWriter
	finalize     sync.Once
	test         *testing.T
	group        *retryAttemptGroup
	execMeta     *testExecutionMetadata
	result       retryAttemptResult
	startTime    time.Time
	subtree      *quarantinedRaceChildState
	coverage     *coverage.ProcessTestCoverage
	aggregate    *coverage.ProcessCoverageProfile
	aggregateErr error
	source       *processRetryTestSource
}

func wrapProcessRetryChildTest(original func(*testing.T), cfg processRetryChildConfig, writer *processRetryResultWriter, control *processRetryControl) (func(*testing.T), func()) {
	observation := &processRetryChildObservation{cfg: cfg, writer: writer}
	wrapped := func(t *testing.T) {
		start := time.Now()
		observation.test = t
		observation.startTime = start
		observation.source = processRetrySourceFromFunc(runtime.FuncForPC(reflect.ValueOf(original).Pointer()))
		if cfg.Subtree != nil && cfg.Subtree.CollectAggregate {
			observation.aggregate, observation.aggregateErr = coverage.BeginProcessCoverageProfile()
		}

		group, reason := newRetryAttemptGroupWithOutputObservation(t, false)
		if reason != "" {
			writer.Write(processRetryNotRunResult(cfg, "testing_t_reflection_drift"))
			t.Fail()
			return
		}
		observation.group = group
		group.rootParallelBridge = control.childRootParallelBridge
		defer group.retire()

		prepare := func(attempt *retryAttemptRoot) string {
			deadline, deadlineOK := control.logicalDeadline()
			if !setRetryAttemptLogicalDeadline(attempt, deadline, deadlineOK) {
				return "testing_t_reflection_drift"
			}
			execMeta := createTestMetadata(attempt.test, nil)
			attempt.metadata = execMeta
			execMeta.identity = newTestIdentity("", "", cfg.TestName)
			execMeta.test = newProcessRetryNoopTest(attempt.test, cfg, start, writer, control, attempt.raceBaseline)
			if cfg.Subtree != nil {
				observation.subtree = newQuarantinedRaceChildState(cfg.Subtree)
				observation.subtree.parallelBridge = control.childRootParallelBridge
				execMeta.quarantinedRaceChild = observation.subtree
				execMeta.processRetryCoverageMu = &sync.Mutex{}
			}
			observation.execMeta = execMeta
			return ""
		}
		childBody := original
		topLevelName, _ := topLevelTestName(cfg.TestName)
		if cfg.Subtree != nil && cfg.Subtree.SelectedRoot == topLevelName {
			childBody = func(t *testing.T) {
				rootDirective := cfg.Subtree.Root
				observation.execMeta.isDisabled = rootDirective.Disabled
				observation.execMeta.isQuarantined = rootDirective.Quarantined
				observation.execMeta.isAttemptToFix = rootDirective.AttemptToFix
				observation.execMeta.isAModifiedTest = rootDirective.Modified
				observation.execMeta.shouldOrchestrateAttemptToFix = cfg.Subtree.OwnsAttemptToFix
				if cfg.Subtree.CollectPerTest && coverage.CanCollect() {
					if fn := runtime.FuncForPC(reflect.ValueOf(original).Pointer()); fn != nil {
						path, _ := fn.FileLine(fn.Entry())
						observation.coverage = coverage.BeginProcessTestCoverage(path)
					}
				}
				if rootDirective.Disabled && !rootDirective.AttemptToFix {
					reason := constants.TestDisabledSkipReason
					observation.execMeta.processRetrySkipReason.Store(&reason)
					t.SkipNow()
				}
				if skip, forced := cfg.Subtree.itrDecision(cfg.Subtree.SelectedRoot, rootDirective, runtime.FuncForPC(reflect.ValueOf(original).Pointer())); skip {
					observation.execMeta.isItrSkipped = true
					reason := constants.SkippedByITRReason
					observation.execMeta.processRetrySkipReason.Store(&reason)
					t.SkipNow()
				} else {
					observation.execMeta.isItrForcedRun = forced
				}
				original(t)
			}
		}
		attempt, result, reason := runFreshRetryAttemptInGroupWithCallbacks(group, prepare, childBody, nil)
		if reason != "" || attempt == nil {
			writer.Write(processRetryNotRunResult(cfg, "testing_t_reflection_drift"))
			t.Fail()
			return
		}
		observation.result = result
		status := processRetryControlledTerminalStatus(result)
		if status != "" {
			observation.writeControlledTerminalResult(control, status)
		} else {
			observation.writeFinalResult()
		}
		if cfg.Subtree != nil && cfg.Subtree.SelectedRoot != topLevelName && status != "" {
			// A controlled terminal here belongs to a discovery ancestor. The
			// nested result (including not_run) is recorded independently, and the
			// parent-process ancestor executes separately.
			return
		}

		if result.failed || result.raceDetected || result.panicData != nil || result.cleanupPanicData != nil {
			t.Fail()
		} else if result.skipped {
			if fields := getTestPrivateFields(t); fields != nil {
				fields.SetSkipped(true)
			}
		}
		if result.nativeFatalTraceReplay {
			replayRetryAttemptNativeTerminalTrace(result.terminalTrace)
		}
		if result.panicData != nil {
			panic(result.panicData)
		}
		if result.cleanupPanicData != nil {
			panic(result.cleanupPanicData)
		}
	}
	return wrapped, observation.writeFinalResult
}

func processRetryControlledTerminalStatus(result retryAttemptResult) processRetryStatus {
	if result.panicData == nil && result.cleanupPanicData == nil {
		return ""
	}
	if result.completionPhase == retryAttemptCompletionUnexpectedGoexit || errors.Is(asError(result.panicData), errRetryAttemptNilPanicOrGoexit) {
		return processRetryStatusControlledUnexpectedGoexitReady
	}
	return processRetryStatusControlledPanicReady
}

func asError(value any) error {
	err, _ := value.(error)
	return err
}

func (o *processRetryChildObservation) writeFinalResult() {
	if o == nil {
		return
	}
	o.finalize.Do(func() {
		if o.test == nil || o.execMeta == nil {
			return
		}
		o.writer.Write(o.buildResult(""))
	})
}

func (o *processRetryChildObservation) writeControlledTerminalResult(control *processRetryControl, status processRetryStatus) {
	if o == nil || !isProcessRetryControlledTerminalStatus(status) {
		return
	}
	written := false
	o.finalize.Do(func() {
		if o.test == nil || o.execMeta == nil {
			return
		}
		result := o.buildResult(status)
		written = o.writer.Write(result) && result.Status == status
	})
	if written && control != nil {
		_ = control.childControlledTerminal(status)
	}
}

func (o *processRetryChildObservation) buildResult(status processRetryStatus) processRetryResult {
	if o.subtree != nil {
		if err := o.subtree.waitParallelBridge(); err != nil {
			return processRetryNotRunResult(o.cfg, "parallel_control_failed")
		}
	}
	finish := o.startTime.Add(o.result.duration)
	panicData := o.result.panicData
	panicStack := o.result.panicStack
	if panicData == nil && o.result.cleanupPanicData != nil {
		panicData = o.result.cleanupPanicData
		panicStack = o.result.cleanupPanicStack
	}
	failed := o.result.failed || o.result.raceDetected || panicData != nil
	rootParallel := false
	if o.group != nil {
		o.group.mu.Lock()
		rootParallel = o.group.rootParallelObserved
		o.group.mu.Unlock()
	}
	result := processRetryResult{
		Version:           1,
		TestName:          o.cfg.TestName,
		Attempt:           o.cfg.Attempt,
		RetryReason:       o.cfg.RetryReason,
		MRunEpoch:         o.cfg.MRunEpoch,
		InvocationOrdinal: o.cfg.InvocationOrdinal,
		StartUnixNano:     o.startTime.UnixNano(),
		FinishUnixNano:    finish.UnixNano(),
		DurationNanos:     o.result.duration.Nanoseconds(),
		Failed:            failed,
		Skipped:           o.result.skipped,
		Panic:             panicData != nil,
		RaceDetected:      o.result.raceDetected,
		RootParallel:      rootParallel,
	}
	if result.Failed {
		if panicInfo := o.execMeta.processRetryPanic.Load(); result.Panic && panicInfo != nil {
			result.ErrorType = panicInfo.Type
			result.ErrorMessage = panicInfo.Message
			result.ErrorStack = panicInfo.Stack
		} else if result.Panic && panicData != nil {
			result.ErrorType = "panic"
			result.ErrorMessage = truncateProcessRetryErrorMessage(toString(panicData))
			result.ErrorStack = truncateProcessRetryErrorStack(string(panicStack))
		} else if errorInfo := o.execMeta.processRetryError.Load(); errorInfo != nil {
			result.ErrorType = errorInfo.Type
			result.ErrorMessage = errorInfo.Message
			result.ErrorStack = errorInfo.Stack
		}
	}
	if result.Skipped && !result.Failed {
		if skipReason := o.execMeta.processRetrySkipReason.Load(); skipReason != nil {
			result.SkipReason = *skipReason
		}
	}
	if o.cfg.Subtree != nil {
		if status == "" {
			switch {
			case result.Failed:
				result.Status = processRetryStatusFail
			case result.Skipped:
				result.Status = processRetryStatusSkip
			default:
				result.Status = processRetryStatusPass
			}
		}
		return o.buildSubtreeResult(result, status)
	}
	if status != "" {
		result.Status = status
		result.Failed = true
		result.Panic = true
		result.Skipped = false
		result.SkipReason = ""
		return result
	}
	switch {
	case result.Failed:
		result.Status = processRetryStatusFail
		result.SkipReason = ""
	case result.Skipped:
		result.Status = processRetryStatusSkip
	default:
		result.Status = processRetryStatusPass
	}
	return result
}

func processRetryChildOwnerMetadata(execMeta *testExecutionMetadata) *testExecutionMetadata {
	if execMeta != nil && execMeta.quarantinedRaceChild != nil {
		return execMeta
	}
	for execMeta != nil && execMeta.processRetryOwner != nil && execMeta.processRetryOwner != execMeta {
		execMeta = execMeta.processRetryOwner
	}
	return execMeta
}

func recordProcessRetryChildErrorInfo(tb testing.TB, errType, errMessage string, stackSkip int) {
	if execMeta := processRetryChildOwnerMetadata(getTestMetadata(tb)); execMeta != nil {
		execMeta.processRetryError.CompareAndSwap(nil, &processRetryErrorInfo{
			Type:    truncateProcessRetryErrorType(errType),
			Message: truncateProcessRetryStructuredErrorMessage(errMessage),
			Stack:   truncateProcessRetryStructuredErrorStack(utils.GetStacktrace(stackSkip)),
		})
	}
}

// markProcessRetryChildFailed mirrors testing.common.Fail's parent-first failure
// propagation without calling a woven testing method again.
func markProcessRetryChildFailed(tb testing.TB) {
	t, ok := tb.(*testing.T)
	if !ok {
		return
	}
	fields := getTestPrivateFields(t)
	ancestry := make([]*commonPrivateFields, 0, 4)
	for fields != nil {
		ancestry = append(ancestry, fields)
		fields = getCommonParentPrivateFields(fields)
	}
	for _, fields := range slices.Backward(ancestry) {
		fields.SetFailed(true)
	}
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
		if owner.quarantinedRaceChild != nil {
			runQuarantinedRaceChildSubtest(t, original, owner)
			return
		}

		execMeta := createTestMetadata(t, nil)
		execMeta.test = owner.test
		execMeta.processRetryOwner = owner
		t.Cleanup(func() { deleteTestMetadata(t) })

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
			owner.processRetryPanic.CompareAndSwap(nil, &processRetryErrorInfo{
				Type:    "panic",
				Message: truncateProcessRetryErrorMessage(toString(panicData)),
				Stack:   truncateProcessRetryErrorStack(utils.GetStacktrace(1)),
			})
			if root, ok := owner.test.(*processRetryNoopTest); ok {
				root.writePanicResult(owner.processRetryPanic.Load())
			}
			if unexpectedTermination {
				return
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
		if fields.GetFinished() {
			return false
		}
		// testing.tRunner treats a non-parallel subtest's Goexit as FailNow
		// propagation when an ancestor already finished.
		if !isParallelTest(t, fields) {
			for parent := getCommonParentPrivateFields(fields); parent != nil; parent = getCommonParentPrivateFields(parent) {
				if parent.GetFinished() {
					return false
				}
			}
		}
		return true
	}
	return !t.Failed() && !t.Skipped()
}

func processRetryRootParallelObserved(t *testing.T) bool {
	fields := getTestPrivateFields(t)
	if fields == nil || fields.mu == nil || fields.isParallel == nil {
		return false
	}
	fields.mu.RLock()
	defer fields.mu.RUnlock()
	return *fields.isParallel
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
	if len(payload) > processRetryWireMaxBytes(len(result.Subtests) > 0 || len(result.Coverage) > 0 || result.Source != nil) {
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

	limit := processRetryWireMaxBytes(expected.Subtree != nil)
	payload, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return processRetryResult{}, false, fmt.Errorf("%w: result file unreadable", errProcessRetryResultInvalid)
	}
	if len(payload) > limit {
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
	if result.TestName != expected.TestName ||
		result.Attempt != expected.Attempt ||
		result.RetryReason != expected.RetryReason ||
		result.MRunEpoch != expected.MRunEpoch ||
		result.InvocationOrdinal != expected.InvocationOrdinal {
		return fmt.Errorf("%w: identity mismatch", errProcessRetryResultInvalid)
	}
	if (result.MRunEpoch == 0) != (result.InvocationOrdinal == 0) {
		return fmt.Errorf("%w: invalid invocation identity", errProcessRetryResultInvalid)
	}
	if !processRetryJSONStringFits(result.ErrorType, processRetryErrorTypeMaxBytes) ||
		!processRetryJSONStringFits(result.ErrorMessage, processRetryErrorMessageMaxBytes) ||
		!processRetryJSONStringFits(result.ErrorStack, processRetryErrorStackMaxBytes) ||
		!processRetryJSONStringFits(result.SkipReason, processRetrySkipReasonMaxBytes) ||
		!processRetryJSONStringFits(result.ResultError, processRetryResultErrorMaxBytes) ||
		!processRetryJSONStringFits(result.OutputTail, processRetrySubtreeOutputMaxBytes) {
		return fmt.Errorf("%w: metadata field too large", errProcessRetryResultInvalid)
	}
	if err := validateProcessRetrySubtreeResultEnvelope(result, expected); err != nil {
		return err
	}
	return validateProcessRetryResultStatus(result)
}

func validateProcessRetryResultStatus(result processRetryResult) error {
	if result.Panic && (result.Status != processRetryStatusFail && !isProcessRetryControlledTerminalStatus(result.Status) || !result.Failed || result.ErrorType == "") {
		return fmt.Errorf("%w: invalid panic mirrors", errProcessRetryResultInvalid)
	}
	if result.RaceDetected && (result.Status != processRetryStatusFail || !result.Failed) {
		return fmt.Errorf("%w: invalid race mirrors", errProcessRetryResultInvalid)
	}
	switch result.Status {
	case processRetryStatusPass:
		if result.Failed || result.Skipped || result.Panic || result.RaceDetected || result.ErrorType != "" || result.ErrorMessage != "" || result.ErrorStack != "" || result.SkipReason != "" || result.ResultError != "" {
			return fmt.Errorf("%w: invalid pass mirrors", errProcessRetryResultInvalid)
		}
	case processRetryStatusSkip:
		if result.Failed || !result.Skipped || result.Panic || result.RaceDetected || result.ErrorType != "" || result.ErrorMessage != "" || result.ErrorStack != "" || result.ResultError != "" {
			return fmt.Errorf("%w: invalid skip mirrors", errProcessRetryResultInvalid)
		}
	case processRetryStatusFail:
		if !result.Failed || result.SkipReason != "" || result.ResultError != "" || (result.ErrorType == "" && (result.ErrorMessage != "" || result.ErrorStack != "")) {
			return fmt.Errorf("%w: invalid fail mirrors", errProcessRetryResultInvalid)
		}
	case processRetryStatusControlledPanicReady, processRetryStatusControlledUnexpectedGoexitReady:
		if !result.Failed || result.Skipped || !result.Panic || result.SkipReason != "" || result.ResultError != "" || result.ErrorType == "" {
			return fmt.Errorf("%w: invalid controlled terminal mirrors", errProcessRetryResultInvalid)
		}
	case processRetryStatusNotRun:
		if result.Failed || result.Skipped || result.Panic || result.RaceDetected || result.RootParallel || result.ErrorType != "" || result.ErrorMessage != "" || result.ErrorStack != "" || result.SkipReason != "" || result.OutputTail != "" || result.OutputTruncated || result.Source != nil || len(result.Coverage) > 0 || len(result.Subtests) > 0 || result.SkippedByITR || result.ITRForcedRun || result.Modified || !validProcessRetryResultError(result.ResultError) {
			return fmt.Errorf("%w: invalid not_run mirrors", errProcessRetryResultInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown status", errProcessRetryResultInvalid)
	}
	return nil
}

func validProcessRetryResultError(reason string) bool {
	switch reason {
	case "", "missing_result_path", "missing_test_name", "missing_attempt", "invalid_attempt", "missing_retry_reason", "invalid_child_config", "testing_m_reflection_drift", "testing_t_reflection_drift", "control_protocol_failure", "selected_root_not_run", "coverage_initialization_failed", "coverage_finalization_failed":
		return true
	default:
		return false
	}
}

var _ integrations.Test = (*processRetryNoopTest)(nil)

type processRetryNoopTest struct {
	integrations.Test
	root      *testing.T
	cfg       processRetryChildConfig
	startTime time.Time
	writer    *processRetryResultWriter
	control   *processRetryControl
	raceBase  int64
}

func newProcessRetryNoopTest(root *testing.T, cfg processRetryChildConfig, startTime time.Time, writer *processRetryResultWriter, control *processRetryControl, raceBase int64) integrations.Test {
	return &processRetryNoopTest{
		Test:      integrations.NewProcessRetryNoopTest(cfg.TestName, startTime),
		root:      root,
		cfg:       cfg,
		startTime: startTime,
		writer:    writer,
		control:   control,
		raceBase:  raceBase,
	}
}

func (t *processRetryNoopTest) writePanicResult(info *processRetryErrorInfo) {
	if t == nil || t.writer == nil || info == nil {
		return
	}
	finish := time.Now()
	if t.writer.Write(processRetryResult{
		Version:           1,
		TestName:          t.cfg.TestName,
		Attempt:           t.cfg.Attempt,
		RetryReason:       t.cfg.RetryReason,
		MRunEpoch:         t.cfg.MRunEpoch,
		InvocationOrdinal: t.cfg.InvocationOrdinal,
		Status:            processRetryStatusControlledPanicReady,
		Failed:            true,
		Panic:             true,
		RaceDetected:      retryAttemptRaceErrors() > t.raceBase,
		RootParallel:      processRetryRootParallelObserved(t.root),
		ErrorType:         info.Type,
		ErrorMessage:      info.Message,
		ErrorStack:        info.Stack,
		StartUnixNano:     t.startTime.UnixNano(),
		FinishUnixNano:    finish.UnixNano(),
		DurationNanos:     finish.Sub(t.startTime).Nanoseconds(),
	}) && t.control != nil {
		_ = t.control.childControlledTerminal(processRetryStatusControlledPanicReady)
	}
}
