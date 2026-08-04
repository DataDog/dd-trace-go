// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package retryprocess

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

var forcedRunChildLaunchRuns atomic.Int32
var coverageFirstAttemptRuns atomic.Int32
var runSelectorSubtestRuns atomic.Int32
var skipSelectorSubtestRuns atomic.Int32
var processExitRuns atomic.Int32
var malformedJSONRuns atomic.Int32
var timeoutRuns atomic.Int32
var outputTimeoutRuns atomic.Int32
var descendantCleanupRuns atomic.Int32
var transportIsolationRuns atomic.Int32
var processRetryBenchmarkRuns atomic.Int32
var processRetryBenchmarkAggregateRuns atomic.Int32
var processRetryBenchmarkCPUSink atomic.Uint64
var parallelEFDRuns atomic.Int32
var attemptToFixRuns atomic.Int32
var testMainBaselineRuns atomic.Int32
var processRetryBenchmarkChildStart time.Time

var processRetryCoverageProfileBlock = regexp.MustCompile(`^(.+):(\d+)\.\d+,(\d+)\.\d+\s+\d+\s+(\d+)$`)

const (
	processRetryChildLogSentinel         = "process-retry-child-output-sentinel"
	processRetryProcessExitLogSentinel   = "process-retry-process-exit-output-sentinel"
	processRetryMalformedJSONLogSentinel = "process-retry-malformed-json-output-sentinel"
	processRetryTimeoutLogSentinel       = "process-retry-timeout-output-sentinel"
	processRetryOutputTimeoutLogSentinel = "process-retry-output-timeout-child-sentinel"
	processRetryDescendantLogSentinel    = "process-retry-descendant-output-sentinel"
	processRetryDescendantHelperLifetime = 30 * time.Second
)

func skipProcessRetryFixtureChildLaunchIneligible(t *testing.T, name string) {
	t.Helper()
	if !gotesting.ProcessRetryContainmentSupported() {
		t.Skipf("process retry %s fixture requires process-tree containment", name)
	}
}

const (
	processRetrySelectorFixtureEnv            = "PROCESS_RETRY_SELECTOR_FIXTURE"
	processRetryProcessExitFixtureEnv         = "PROCESS_RETRY_PROCESS_EXIT_FIXTURE"
	processRetryMalformedJSONFixtureEnv       = "PROCESS_RETRY_MALFORMED_JSON_FIXTURE"
	processRetryTimeoutFixtureEnv             = "PROCESS_RETRY_TIMEOUT_FIXTURE"
	processRetryOutputTimeoutFixtureEnv       = "PROCESS_RETRY_OUTPUT_TIMEOUT_FIXTURE"
	processRetryTimeoutReadyPathEnv           = "PROCESS_RETRY_TIMEOUT_READY_PATH"
	processRetryDescendantCleanupFixtureEnv   = "PROCESS_RETRY_DESCENDANT_CLEANUP_FIXTURE"
	processRetryDescendantHelperEnv           = "PROCESS_RETRY_DESCENDANT_HELPER"
	processRetryDescendantLivenessPathEnv     = "PROCESS_RETRY_DESCENDANT_LIVENESS_PATH"
	processRetryDescendantIndependentPathEnv  = "PROCESS_RETRY_DESCENDANT_INDEPENDENT_LIVENESS_PATH"
	processRetryTransportIsolationEnv         = "PROCESS_RETRY_TRANSPORT_ISOLATION_FIXTURE"
	processRetryTransportProbeEnv             = "PROCESS_RETRY_TRANSPORT_PROBE"
	processRetryParallelEFDEnv                = "PROCESS_RETRY_PARALLEL_EFD_FIXTURE"
	processRetryParallelEFDCoordinationDirEnv = "PROCESS_RETRY_PARALLEL_EFD_COORDINATION_DIR"
	processRetryAttemptToFixEnv               = "PROCESS_RETRY_ATTEMPT_TO_FIX_FIXTURE"
	processRetryScenarioEnv                   = "PROCESS_RETRY_FIXTURE_SCENARIO"
	processRetryControllerProbeEnv            = "PROCESS_RETRY_CONTROLLER_PROBE"
	processRetryControllerProbePathEnv        = "PROCESS_RETRY_CONTROLLER_PROBE_PATH"
	processRetryBenchmarkExecutionModeEnv     = "PROCESS_RETRY_BENCHMARK_EXECUTION_MODE"
	processRetryBenchmarkRetryCountEnv        = "PROCESS_RETRY_BENCHMARK_RETRY_COUNT"
	processRetryBenchmarkChildStartupDelayEnv = "PROCESS_RETRY_BENCHMARK_CHILD_STARTUP_DELAY"
	processRetryBenchmarkBodyDelayEnv         = "PROCESS_RETRY_BENCHMARK_BODY_DELAY"
	processRetryBenchmarkCPUWorkEnv           = "PROCESS_RETRY_BENCHMARK_CPU_WORK"
	processRetryBenchmarkAggregateEnv         = "PROCESS_RETRY_BENCHMARK_AGGREGATE"
	processRetryBenchmarkMetricsPathEnv       = "PROCESS_RETRY_BENCHMARK_METRICS_PATH"
	processRetryBenchmarkPassingEnv           = "PROCESS_RETRY_BENCHMARK_PASSING"
	processRetryBenchmarkRetriesEnabledEnv    = "PROCESS_RETRY_BENCHMARK_RETRIES_ENABLED"
	processRetryStartupFixtureEnv             = "PROCESS_RETRY_STARTUP_FIXTURE"
	processRetryStartupRerunFileEnv           = "PROCESS_RETRY_STARTUP_RERUN_FILE"
	processRetryStartupConflictFileEnv        = "PROCESS_RETRY_STARTUP_CONFLICT_FILE"
	processRetryStartupConflictMarkerEnv      = "PROCESS_RETRY_STARTUP_CONFLICT_MARKER_FILE"
	processRetryTestMainFixtureEnv            = "PROCESS_RETRY_TESTMAIN_BASELINE_FIXTURE"
	processRetryTestMainCwdEnv                = "PROCESS_RETRY_TESTMAIN_BASELINE_EXPECTED_CWD"
	processRetryTestMainMarkerEnv             = "PROCESS_RETRY_TESTMAIN_BASELINE_APPLIED"
	processRetryTestMainWorkDir               = "testmain-workdir"
	processRetryDeferredOrderingEnv           = "PROCESS_RETRY_DEFERRED_ORDERING_FIXTURE"
	processRetryDeferredOrderingPathEnv       = "PROCESS_RETRY_DEFERRED_ORDERING_PATH"
	processRetryDeferredRepeatedOrderingEnv   = "PROCESS_RETRY_DEFERRED_REPEATED_ORDERING_FIXTURE"
	processRetryDeferredFTRFailfastPathEnv    = "PROCESS_RETRY_DEFERRED_FTR_FAILFAST_PATH"
)

var (
	startupRerunRuns    atomic.Int32
	startupConflictRuns atomic.Int32
	startupConflictFile *os.File
)

func init() {
	if processRetryFixtureChild() && processRetryFixtureEnv(processRetryBenchmarkExecutionModeEnv) != "" {
		processRetryBenchmarkChildStart = time.Now()
		time.Sleep(processRetryBenchmarkDuration(processRetryBenchmarkChildStartupDelayEnv))
	}
	if path := processRetryFixtureEnv(processRetryStartupRerunFileEnv); path != "" {
		appendStartupFixtureLine(path, "init")
	}
	if path := processRetryFixtureEnv(processRetryStartupConflictFileEnv); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if processRetryFixtureChild() {
				appendStartupFixtureLine(processRetryFixtureEnv(processRetryStartupConflictMarkerEnv), "child_conflict")
			} else {
				appendStartupFixtureLine(processRetryFixtureEnv(processRetryStartupConflictMarkerEnv), "parent_conflict")
			}
			return
		}
		startupConflictFile = file
	}
}

func processRetryBenchmarkDuration(name string) time.Duration {
	value := processRetryFixtureEnv(name)
	if value == "" {
		return 0
	}
	delay, err := time.ParseDuration(value)
	if err != nil || delay < 0 {
		panic(fmt.Sprintf("invalid %s value %q", name, value))
	}
	return delay
}

func processRetryBenchmarkCPUWork() {
	value := processRetryFixtureEnv(processRetryBenchmarkCPUWorkEnv)
	if value == "" {
		return
	}
	iterations, err := strconv.Atoi(value)
	if err != nil || iterations < 0 {
		panic(fmt.Sprintf("invalid %s value %q", processRetryBenchmarkCPUWorkEnv, value))
	}
	workers := max(runtime.GOMAXPROCS(0), 1)
	results := make([]uint64, workers)
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Go(func() {
			start := iterations * worker / workers
			end := iterations * (worker + 1) / workers
			result := uint64(worker + 1)
			for i := start; i < end; i++ {
				result = result*6364136223846793005 + uint64(i) + 1442695040888963407
			}
			results[worker] = result
		})
	}
	wg.Wait()
	var result uint64
	for _, workerResult := range results {
		result ^= workerResult
	}
	processRetryBenchmarkCPUSink.Store(result)
}

func runProcessRetryBenchmarkWork() {
	child := processRetryFixtureChild()
	start := time.Now()
	if child && !processRetryBenchmarkChildStart.IsZero() {
		start = processRetryBenchmarkChildStart
	}
	time.Sleep(processRetryBenchmarkDuration(processRetryBenchmarkBodyDelayEnv))
	processRetryBenchmarkCPUWork()
	if processRetryFixtureEnv(processRetryBenchmarkAggregateEnv) == "true" {
		processRetryBenchmarkAggregateRuns.Add(1)
		return
	}
	observation := processRetryBenchmarkObservation{
		PID:             os.Getpid(),
		ProcessIdentity: "parent",
		Child:           child,
		GOMAXPROCS:      runtime.GOMAXPROCS(0),
		StartUnixNano:   start.UnixNano(),
		FinishUnixNano:  time.Now().UnixNano(),
	}
	if child {
		identity, ok := integrations.LookupProcessRetryChildTransport(constants.CIVisibilityInternalRetryProcessResultPath)
		if !ok || identity == "" {
			panic("process retry benchmark child is missing its process identity")
		}
		observation.ProcessIdentity = identity
	}
	if err := writeProcessRetryBenchmarkObservation(processRetryFixtureEnv(processRetryBenchmarkMetricsPathEnv), observation); err != nil {
		panic(fmt.Sprintf("write process retry benchmark observation: %v", err))
	}
}

type processRetryBenchmarkObservation struct {
	PID             int    `json:"pid"`
	ProcessIdentity string `json:"process_identity"`
	Child           bool   `json:"child"`
	GOMAXPROCS      int    `json:"gomaxprocs"`
	StartUnixNano   int64  `json:"start_unix_nano"`
	FinishUnixNano  int64  `json:"finish_unix_nano"`
}

type processRetryBenchmarkMetrics struct {
	RunMDurationNanos int64                              `json:"runm_duration_nanos"`
	ExecutionCount    int                                `json:"execution_count"`
	Observations      []processRetryBenchmarkObservation `json:"observations"`
}

func (m processRetryBenchmarkMetrics) runMDuration() time.Duration {
	return time.Duration(m.RunMDurationNanos)
}

func (m processRetryBenchmarkMetrics) retryCount(testCount int) int {
	return m.ExecutionCount - testCount
}

func (m processRetryBenchmarkMetrics) childProcessCount() int {
	identities := make(map[string]struct{})
	for _, observation := range m.Observations {
		if observation.Child {
			identities[observation.ProcessIdentity] = struct{}{}
		}
	}
	return len(identities)
}

func (m processRetryBenchmarkMetrics) maxChildOverlap() int {
	type transition struct {
		at    int64
		delta int
	}
	transitions := make([]transition, 0, len(m.Observations)*2)
	for _, observation := range m.Observations {
		if !observation.Child {
			continue
		}
		transitions = append(transitions,
			transition{at: observation.StartUnixNano, delta: 1},
			transition{at: observation.FinishUnixNano, delta: -1},
		)
	}
	sort.Slice(transitions, func(i, j int) bool {
		if transitions[i].at == transitions[j].at {
			return transitions[i].delta < transitions[j].delta
		}
		return transitions[i].at < transitions[j].at
	})
	current, maximum := 0, 0
	for _, transition := range transitions {
		current += transition.delta
		maximum = max(maximum, current)
	}
	return maximum
}

func processRetryBenchmarkEventsDir(path string) string {
	return path + ".events"
}

func writeProcessRetryBenchmarkObservation(path string, observation processRetryBenchmarkObservation) error {
	if path == "" {
		return nil
	}
	dir := processRetryBenchmarkEventsDir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, fmt.Sprintf("%d-*.json", observation.PID))
	if err != nil {
		return err
	}
	name := file.Name()
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func writeProcessRetryBenchmarkMetrics(path string, duration time.Duration) error {
	entries, err := os.ReadDir(processRetryBenchmarkEventsDir(path))
	if err != nil {
		return err
	}
	metrics := processRetryBenchmarkMetrics{RunMDurationNanos: duration.Nanoseconds()}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(processRetryBenchmarkEventsDir(path), entry.Name()))
		if err != nil {
			return err
		}
		var observation processRetryBenchmarkObservation
		if err := json.Unmarshal(payload, &observation); err != nil {
			return err
		}
		metrics.Observations = append(metrics.Observations, observation)
	}
	metrics.ExecutionCount = len(metrics.Observations)
	if processRetryFixtureEnv(processRetryBenchmarkAggregateEnv) == "true" {
		metrics.ExecutionCount = int(processRetryBenchmarkAggregateRuns.Load())
	}
	payload, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

func readProcessRetryBenchmarkMetrics(path string) (processRetryBenchmarkMetrics, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return processRetryBenchmarkMetrics{}, err
	}
	var metrics processRetryBenchmarkMetrics
	if err := json.Unmarshal(payload, &metrics); err != nil {
		return processRetryBenchmarkMetrics{}, err
	}
	if metrics.RunMDurationNanos < 0 {
		return processRetryBenchmarkMetrics{}, fmt.Errorf("invalid process retry benchmark duration %d", metrics.RunMDurationNanos)
	}
	if metrics.ExecutionCount < 0 {
		return processRetryBenchmarkMetrics{}, fmt.Errorf("invalid process retry benchmark execution count %d", metrics.ExecutionCount)
	}
	return metrics, nil
}

func TestProcessRetryBenchmarkMetricsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.json")
	if err := os.Mkdir(processRetryBenchmarkEventsDir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	wantObservation := processRetryBenchmarkObservation{
		PID: 42, ProcessIdentity: "child-1", Child: true, GOMAXPROCS: 2,
		StartUnixNano: 100, FinishUnixNano: 200,
	}
	if err := writeProcessRetryBenchmarkObservation(path, wantObservation); err != nil {
		t.Fatal(err)
	}
	wantDuration := 123456789 * time.Nanosecond
	if err := writeProcessRetryBenchmarkMetrics(path, wantDuration); err != nil {
		t.Fatal(err)
	}
	got, err := readProcessRetryBenchmarkMetrics(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.runMDuration() != wantDuration {
		t.Fatalf("benchmark RunM duration = %s, want %s", got.runMDuration(), wantDuration)
	}
	if len(got.Observations) != 1 || got.Observations[0] != wantObservation {
		t.Fatalf("benchmark observations = %+v, want %+v", got.Observations, wantObservation)
	}
	if got.ExecutionCount != 1 {
		t.Fatalf("benchmark execution count = %d, want 1", got.ExecutionCount)
	}
}

func TestProcessRetryBenchmarkMetricsUseObservedProcessesAndOverlap(t *testing.T) {
	metrics := processRetryBenchmarkMetrics{ExecutionCount: 3, Observations: []processRetryBenchmarkObservation{
		{PID: 1, ProcessIdentity: "parent", GOMAXPROCS: 2, StartUnixNano: 100, FinishUnixNano: 300},
		{PID: 2, ProcessIdentity: "child-1", Child: true, GOMAXPROCS: 2, StartUnixNano: 120, FinishUnixNano: 220},
		{PID: 3, ProcessIdentity: "child-2", Child: true, GOMAXPROCS: 2, StartUnixNano: 180, FinishUnixNano: 260},
	}}

	if got := metrics.retryCount(1); got != 2 {
		t.Fatalf("benchmark retries = %d, want 2", got)
	}
	if got := metrics.childProcessCount(); got != 2 {
		t.Fatalf("benchmark child processes = %d, want 2", got)
	}
	if got := metrics.maxChildOverlap(); got != 2 {
		t.Fatalf("benchmark maximum child overlap = %d, want 2", got)
	}
}

func runProcessRetryBenchmarkFixture(tb testing.TB, env []string, metricsPath string, cpu, testCount int) processRetryBenchmarkMetrics {
	tb.Helper()
	if err := os.Mkdir(processRetryBenchmarkEventsDir(metricsPath), 0o700); err != nil {
		tb.Fatal(err)
	}
	args := []string{"-test.run=^TestProcessRetryBenchmarkFixture$", "-test.count=" + strconv.Itoa(testCount)}
	if cpu > 0 {
		args = append(args, "-test.cpu="+strconv.Itoa(cpu))
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(env, processRetryBenchmarkMetricsPathEnv+"="+metricsPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("retry benchmark subprocess failed: %v\n%s", err, output)
	}
	metrics, err := readProcessRetryBenchmarkMetrics(metricsPath)
	if err != nil {
		tb.Fatalf("read retry benchmark metrics: %v\n%s", err, output)
	}
	return metrics
}

func validateProcessRetryBenchmarkMetrics(tb testing.TB, metrics processRetryBenchmarkMetrics, testCount, retryCount, childProcesses, gomaxprocs int) {
	tb.Helper()
	wantObservations := testCount + retryCount
	if metrics.ExecutionCount != wantObservations {
		tb.Fatalf("benchmark executions = %d, want %d", metrics.ExecutionCount, wantObservations)
	}
	if len(metrics.Observations) != wantObservations {
		tb.Fatalf("benchmark observations = %d, want %d: %+v", len(metrics.Observations), wantObservations, metrics.Observations)
	}
	if got := metrics.retryCount(testCount); got != retryCount {
		tb.Fatalf("benchmark retries = %d, want %d", got, retryCount)
	}
	if got := metrics.childProcessCount(); got != childProcesses {
		tb.Fatalf("benchmark child processes = %d, want %d: %+v", got, childProcesses, metrics.Observations)
	}
	childObservations := 0
	for _, observation := range metrics.Observations {
		if observation.PID <= 0 || observation.ProcessIdentity == "" || observation.GOMAXPROCS <= 0 || observation.FinishUnixNano < observation.StartUnixNano {
			tb.Fatalf("invalid benchmark observation: %+v", observation)
		}
		if observation.Child {
			childObservations++
		}
		if gomaxprocs > 0 && observation.GOMAXPROCS != gomaxprocs {
			tb.Fatalf("benchmark GOMAXPROCS = %d, want %d: %+v", observation.GOMAXPROCS, gomaxprocs, observation)
		}
	}
	if childObservations != childProcesses {
		tb.Fatalf("benchmark child observations = %d, want %d", childObservations, childProcesses)
	}
}

func validateProcessRetryBenchmarkAggregateMetrics(tb testing.TB, metrics processRetryBenchmarkMetrics, testCount int) {
	tb.Helper()
	if metrics.ExecutionCount != testCount {
		tb.Fatalf("benchmark executions = %d, want %d", metrics.ExecutionCount, testCount)
	}
	if len(metrics.Observations) != 0 {
		tb.Fatalf("aggregate benchmark wrote %d per-test observations, want none", len(metrics.Observations))
	}
}

func TestProcessRetryBenchmarkFixturePreservesTestCPU(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "benchmark CPU propagation")
	for _, cpu := range []int{1, 2} {
		t.Run("gomaxprocs="+strconv.Itoa(cpu), func(t *testing.T) {
			metrics := runProcessRetryBenchmarkFixture(
				t,
				processRetryScenarioEnvironment(processRetryBenchmarkExecutionModeEnv+"=process"),
				filepath.Join(t.TempDir(), "metrics.json"),
				cpu,
				1,
			)
			validateProcessRetryBenchmarkMetrics(t, metrics, 1, 1, 1, cpu)
		})
	}
}

func TestProcessRetryBenchmarkPassingFixtureDoesNotRetry(t *testing.T) {
	for _, mode := range []string{"in_process", "process"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "process" {
				skipProcessRetryFixtureChildLaunchIneligible(t, "passing benchmark fixture")
			}
			metrics := runProcessRetryBenchmarkFixture(
				t,
				processRetryScenarioEnvironment(
					processRetryBenchmarkExecutionModeEnv+"="+mode,
					processRetryBenchmarkPassingEnv+"=true",
					processRetryBenchmarkRetriesEnabledEnv+"=true",
				),
				filepath.Join(t.TempDir(), "metrics.json"),
				0,
				3,
			)
			validateProcessRetryBenchmarkMetrics(t, metrics, 3, 0, 0, 0)
		})
	}
}

func TestProcessRetryBenchmarkAggregateFixtureAvoidsPerTestObservationIO(t *testing.T) {
	metricsPath := filepath.Join(t.TempDir(), "metrics.json")
	metrics := runProcessRetryBenchmarkFixture(
		t,
		processRetryScenarioEnvironment(
			processRetryBenchmarkExecutionModeEnv+"=process",
			processRetryBenchmarkPassingEnv+"=true",
			processRetryBenchmarkRetriesEnabledEnv+"=true",
			processRetryBenchmarkAggregateEnv+"=true",
		),
		metricsPath,
		0,
		3,
	)
	validateProcessRetryBenchmarkAggregateMetrics(t, metrics, 3)
	entries, err := os.ReadDir(processRetryBenchmarkEventsDir(metricsPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("aggregate benchmark wrote %d per-test observations, want none", len(entries))
	}
}

// Subprocess benchmark allocations belong to the controller process. Use the
// observed runm and child-process metrics for comparisons across retry modes.
func BenchmarkProcessRetryExecutionMode(b *testing.B) {
	for _, mode := range []string{"in_process", "process"} {
		b.Run(mode, func(b *testing.B) {
			if mode == "process" && !gotesting.ProcessRetryContainmentSupported() {
				b.Skip("process retry benchmark requires process-tree containment")
			}
			metricsDir := b.TempDir()
			env := processRetryScenarioEnvironment(processRetryBenchmarkExecutionModeEnv + "=" + mode)
			warmup := runProcessRetryBenchmarkFixture(b, env, filepath.Join(metricsDir, "warmup"), 0, 1)
			childProcesses := 0
			if mode == "process" {
				childProcesses = 1
			}
			validateProcessRetryBenchmarkMetrics(b, warmup, 1, 1, childProcesses, 0)
			var runMTotal time.Duration
			var retryTotal, childProcessTotal int
			b.ResetTimer()
			for i := range b.N {
				metrics := runProcessRetryBenchmarkFixture(b, env, filepath.Join(metricsDir, strconv.Itoa(i)), 0, 1)
				validateProcessRetryBenchmarkMetrics(b, metrics, 1, 1, childProcesses, 0)
				runMTotal += metrics.runMDuration()
				retryTotal += metrics.retryCount(1)
				childProcessTotal += metrics.childProcessCount()
			}
			b.StopTimer()
			b.ReportMetric(float64(retryTotal)/float64(b.N), "retries/op")
			b.ReportMetric(float64(runMTotal)/float64(b.N)/float64(time.Millisecond), "runm-ms/op")
			b.ReportMetric(float64(childProcessTotal)/float64(b.N), "retry-child-processes/op")
		})
	}
}

func BenchmarkProcessRetryNoRetryHotPath(b *testing.B) {
	const testsPerProcess = 100
	cases := []struct {
		name           string
		mode           string
		retriesEnabled bool
	}{
		{name: "ci_visibility", mode: "in_process"},
		{name: "in_process", mode: "in_process", retriesEnabled: true},
		{name: "process", mode: "process", retriesEnabled: true},
	}
	for _, benchmarkCase := range cases {
		b.Run(benchmarkCase.name, func(b *testing.B) {
			metricsDir := b.TempDir()
			env := processRetryScenarioEnvironment(
				processRetryBenchmarkExecutionModeEnv+"="+benchmarkCase.mode,
				processRetryBenchmarkPassingEnv+"=true",
				processRetryBenchmarkRetriesEnabledEnv+"="+strconv.FormatBool(benchmarkCase.retriesEnabled),
				processRetryBenchmarkAggregateEnv+"=true",
			)
			warmup := runProcessRetryBenchmarkFixture(b, env, filepath.Join(metricsDir, "warmup"), 0, 1)
			validateProcessRetryBenchmarkAggregateMetrics(b, warmup, 1)
			var runMTotal time.Duration
			b.ResetTimer()
			for i := range b.N {
				metrics := runProcessRetryBenchmarkFixture(b, env, filepath.Join(metricsDir, strconv.Itoa(i)), 0, testsPerProcess)
				validateProcessRetryBenchmarkAggregateMetrics(b, metrics, testsPerProcess)
				runMTotal += metrics.runMDuration()
			}
			b.StopTimer()
			b.ReportMetric(float64(runMTotal)/float64(b.N*testsPerProcess), "runm-ns/test")
			b.ReportMetric(0, "retry-child-processes/op")
		})
	}
}

func BenchmarkProcessRetryEFD(b *testing.B) {
	profiles := []struct {
		name         string
		startupDelay time.Duration
		bodyDelay    time.Duration
	}{
		{name: "startup_dominated", startupDelay: 250 * time.Millisecond, bodyDelay: 10 * time.Millisecond},
		{name: "body_dominated", startupDelay: 10 * time.Millisecond, bodyDelay: 250 * time.Millisecond},
	}
	cases := []struct {
		name               string
		mode               string
		parallel           bool
		processConcurrency int
	}{
		{name: "in_process/sequential", mode: "in_process", processConcurrency: 1},
		{name: "in_process/parallel", mode: "in_process", parallel: true, processConcurrency: 1},
		{name: "process/sequential", mode: "process", processConcurrency: 1},
		{name: "process/parallel/concurrency=2", mode: "process", parallel: true, processConcurrency: 2},
		{name: "process/parallel/concurrency=8", mode: "process", parallel: true, processConcurrency: 8},
		{name: "process/parallel/default", mode: "process", parallel: true, processConcurrency: 4},
	}

	for _, profile := range profiles {
		b.Run(profile.name, func(b *testing.B) {
			for _, retryCount := range []int{2, 5, 10} {
				b.Run(fmt.Sprintf("retries=%d", retryCount), func(b *testing.B) {
					for _, benchmarkCase := range cases {
						b.Run(benchmarkCase.name, func(b *testing.B) {
							if benchmarkCase.mode == "process" && !gotesting.ProcessRetryContainmentSupported() {
								b.Skip("process retry benchmark requires process-tree containment")
							}
							metricsDir := b.TempDir()
							maxConcurrency := strconv.Itoa(benchmarkCase.processConcurrency)
							if benchmarkCase.name == "process/parallel/default" {
								maxConcurrency = ""
							}
							env := processRetryScenarioEnvironment(
								processRetryBenchmarkExecutionModeEnv+"="+benchmarkCase.mode,
								processRetryParallelEFDEnv+"=true",
								processRetryBenchmarkRetryCountEnv+"="+strconv.Itoa(retryCount),
								processRetryBenchmarkChildStartupDelayEnv+"="+profile.startupDelay.String(),
								processRetryBenchmarkBodyDelayEnv+"="+profile.bodyDelay.String(),
								constants.CIVisibilityInternalParallelEarlyFlakeDetectionEnabled+"="+strconv.FormatBool(benchmarkCase.parallel),
								constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable+"="+maxConcurrency,
							)
							childProcesses := 0
							if benchmarkCase.mode == "process" {
								childProcesses = retryCount
							}
							warmup := runProcessRetryBenchmarkFixture(b, env, filepath.Join(metricsDir, "warmup"), 0, 1)
							validateProcessRetryBenchmarkMetrics(b, warmup, 1, retryCount, childProcesses, 0)
							var runMTotal time.Duration
							var retryTotal, childProcessTotal, maximumOverlap int
							b.ResetTimer()
							for i := range b.N {
								metrics := runProcessRetryBenchmarkFixture(b, env, filepath.Join(metricsDir, strconv.Itoa(i)), 0, 1)
								validateProcessRetryBenchmarkMetrics(b, metrics, 1, retryCount, childProcesses, 0)
								runMTotal += metrics.runMDuration()
								retryTotal += metrics.retryCount(1)
								childProcessTotal += metrics.childProcessCount()
								maximumOverlap = max(maximumOverlap, metrics.maxChildOverlap())
							}
							b.StopTimer()
							b.ReportMetric(float64(retryTotal)/float64(b.N), "retries/op")
							b.ReportMetric(float64(runMTotal)/float64(b.N)/float64(time.Millisecond), "runm-ms/op")
							b.ReportMetric(float64(profile.startupDelay.Milliseconds()), "configured-child-startup-ms/retry")
							b.ReportMetric(float64(profile.bodyDelay.Milliseconds()), "configured-body-ms/execution")
							b.ReportMetric(float64(maximumOverlap), "observed-max-process-concurrency")
							b.ReportMetric(float64(childProcessTotal)/float64(b.N), "retry-child-processes/op")
						})
					}
				})
			}
		})
	}
}

func BenchmarkProcessRetryParallelEFDCPU(b *testing.B) {
	const (
		retryCount = 4
		cpuWork    = 500_000_000
	)
	cases := []struct {
		name               string
		mode               string
		parallel           bool
		processConcurrency string
	}{
		{name: "in_process/sequential", mode: "in_process", processConcurrency: "1"},
		{name: "process/sequential", mode: "process", processConcurrency: "1"},
		{name: "process/parallel/default", mode: "process", parallel: true},
	}
	for _, cpu := range []int{1, 2, 4} {
		b.Run("gomaxprocs="+strconv.Itoa(cpu), func(b *testing.B) {
			for _, benchmarkCase := range cases {
				b.Run(benchmarkCase.name, func(b *testing.B) {
					if benchmarkCase.mode == "process" && !gotesting.ProcessRetryContainmentSupported() {
						b.Skip("process retry benchmark requires process-tree containment")
					}
					metricsDir := b.TempDir()
					env := processRetryScenarioEnvironment(
						processRetryBenchmarkExecutionModeEnv+"="+benchmarkCase.mode,
						processRetryParallelEFDEnv+"=true",
						processRetryBenchmarkRetryCountEnv+"="+strconv.Itoa(retryCount),
						processRetryBenchmarkCPUWorkEnv+"="+strconv.Itoa(cpuWork),
						constants.CIVisibilityInternalParallelEarlyFlakeDetectionEnabled+"="+strconv.FormatBool(benchmarkCase.parallel),
						constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable+"="+benchmarkCase.processConcurrency,
					)
					childProcesses := 0
					if benchmarkCase.mode == "process" {
						childProcesses = retryCount
					}
					warmup := runProcessRetryBenchmarkFixture(b, env, filepath.Join(metricsDir, "warmup"), cpu, 1)
					validateProcessRetryBenchmarkMetrics(b, warmup, 1, retryCount, childProcesses, cpu)
					var runMTotal time.Duration
					var retryTotal, childProcessTotal, maximumOverlap int
					b.ResetTimer()
					for i := range b.N {
						metrics := runProcessRetryBenchmarkFixture(b, env, filepath.Join(metricsDir, strconv.Itoa(i)), cpu, 1)
						validateProcessRetryBenchmarkMetrics(b, metrics, 1, retryCount, childProcesses, cpu)
						runMTotal += metrics.runMDuration()
						retryTotal += metrics.retryCount(1)
						childProcessTotal += metrics.childProcessCount()
						maximumOverlap = max(maximumOverlap, metrics.maxChildOverlap())
					}
					b.StopTimer()
					b.ReportMetric(float64(runMTotal)/float64(b.N)/float64(time.Millisecond), "runm-ms/op")
					b.ReportMetric(float64(retryTotal)/float64(b.N), "retries/op")
					b.ReportMetric(float64(cpu), "observed-gomaxprocs")
					b.ReportMetric(float64(maximumOverlap), "observed-max-process-concurrency")
					b.ReportMetric(float64(childProcessTotal)/float64(b.N), "retry-child-processes/op")
				})
			}
		})
	}
}

func TestProcessRetryBenchmarkFixture(t *testing.T) {
	mode := processRetryFixtureEnv(processRetryBenchmarkExecutionModeEnv)
	if mode == "" {
		t.Skip("benchmark fixture runs only from its benchmark subprocess")
	}
	if processRetryFixtureChild() {
		if mode != "process" {
			t.Fatalf("%s retry unexpectedly launched a child process", mode)
		}
		runProcessRetryBenchmarkWork()
		return
	}

	runProcessRetryBenchmarkWork()
	if processRetryFixtureEnv(processRetryBenchmarkPassingEnv) == "true" {
		return
	}
	run := processRetryBenchmarkRuns.Add(1)
	if run == 1 {
		t.Fail()
		return
	}
	if mode == "process" {
		t.Fatal("process retry ran in the parent process")
	}
	if mode != "in_process" {
		t.Fatalf("unknown retry execution mode %q", mode)
	}
}

func TestProcessRetryControllersAreNotRetried(t *testing.T) {
	if processRetryFixtureEnv(processRetryControllerProbeEnv) == "true" {
		path := processRetryFixtureEnv(processRetryControllerProbePathEnv)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("x"); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("controller probe fails intentionally")
	}

	path := filepath.Join(t.TempDir(), "controller-runs")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryControllersAreNotRetried$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		processRetryControllerProbeEnv+"=true",
		processRetryControllerProbePathEnv+"="+path,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("controller probe unexpectedly passed:\n%s", output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x" {
		t.Fatalf("controller probe was retried, got run markers %q", data)
	}
}

func TestDeferredProcessRetryFirstPassCompletesBeforeRetryStarts(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "deferred first-pass ordering")
	path := filepath.Join(t.TempDir(), "order")
	runProcessRetryFixtureSubprocess(t, "deferred-first-pass-ordering", []string{
		"-test.run=^(TestDeferredProcessRetryOrderingA|TestDeferredProcessRetryOrderingB)$",
		"-test.v",
	}, processRetryDeferredOrderingEnv+"=true", processRetryDeferredOrderingPathEnv+"="+path)
}

func TestDeferredProcessRetryPhasesRemainOrderedAcrossCount(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "deferred repeated-phase ordering")
	path := filepath.Join(t.TempDir(), "order")
	runProcessRetryFixtureSubprocess(t, "deferred-repeated-phase-ordering", []string{
		"-test.run=^(TestDeferredProcessRetryOrderingA|TestDeferredProcessRetryOrderingB)$",
		"-test.count=2",
		"-test.v",
	},
		processRetryDeferredOrderingEnv+"=true",
		processRetryDeferredOrderingPathEnv+"="+path,
		processRetryDeferredRepeatedOrderingEnv+"=true",
	)
}

func TestDeferredProcessRetryOrderingA(t *testing.T) {
	if processRetryFixtureEnv(processRetryDeferredOrderingEnv) != "true" {
		t.Skip("deferred ordering fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		recordDeferredProcessRetryOrder(t, "A:retry")
		return
	}
	recordDeferredProcessRetryOrder(t, "A:first")
	t.Fail()
}

func TestDeferredProcessRetryOrderingB(t *testing.T) {
	if processRetryFixtureEnv(processRetryDeferredOrderingEnv) != "true" || processRetryFixtureChild() {
		t.Skip("deferred ordering fixture runs only during the parent first pass")
	}
	recordDeferredProcessRetryOrder(t, "B:first")
}

func recordDeferredProcessRetryOrder(t *testing.T, entry string) {
	t.Helper()
	file, err := os.OpenFile(processRetryFixtureEnv(processRetryDeferredOrderingPathEnv), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open deferred process retry order for %q: %v", entry, err)
	}
	if _, err := file.WriteString(entry + "\n"); err != nil {
		_ = file.Close()
		t.Fatalf("record deferred process retry order %q: %v", entry, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close deferred process retry order after %q: %v", entry, err)
	}
}

func TestProcessRetryFocusedMainAssertionsController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "focused main assertions")
	const testName = "TestProcessRetryITRForcedRun"
	runProcessRetryFixtureSubprocess(t, testName, []string{"-test.run=^" + testName + "$", "-test.v"})
}

//dd:test.unskippable
func TestProcessRetryITRForcedRun(t *testing.T) {
	if !processRetryFixtureScenarioEnabled() && !processRetryFixtureChild() {
		t.Skip("process retry fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if forcedRunChildLaunchRuns.Load() != 0 {
			t.Fatalf("process retry child inherited forced-run parent count: %d", forcedRunChildLaunchRuns.Load())
		}
		fmt.Println(processRetryChildLogSentinel)
		return
	}
	if forcedRunChildLaunchRuns.Add(1) == 1 {
		t.Fatal("first forced-run parent execution must fail to trigger process retry")
	}
	t.Fatalf("forced-run retry ran in the parent process with run count %d", forcedRunChildLaunchRuns.Load())
}

func TestProcessRetryAttemptToFixController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "attempt to fix")
	runProcessRetryFixtureSubprocess(t, "attempt-to-fix", []string{
		"-test.run=^TestProcessRetryAttemptToFixParent$",
		"-test.v",
	},
		processRetryAttemptToFixEnv+"=true",
		constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable+"=3",
	)
}

func TestProcessRetryAttemptToFixParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryAttemptToFixEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("attempt-to-fix fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if run := attemptToFixRuns.Add(1); run != 1 {
			t.Fatalf("attempt-to-fix child executed the selected attempt %d times", run)
		}
		reason, ok := integrations.LookupProcessRetryChildTransport(constants.CIVisibilityInternalRetryProcessReason)
		if !ok || reason != constants.AttemptToFixRetryReason {
			t.Fatalf("attempt-to-fix child retry reason = %q, want %q", reason, constants.AttemptToFixRetryReason)
		}
		fmt.Println(processRetryChildLogSentinel)
		return
	}
	if run := attemptToFixRuns.Add(1); run != 1 {
		t.Fatalf("attempt-to-fix retry ran in the parent process with run count %d", run)
	}
}

func TestDeferredProcessRetryAttemptToFixFailfastController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "deferred attempt-to-fix failfast")
	cmd := exec.Command(
		os.Args[0],
		"-test.run=^TestDeferredProcessRetryAttemptToFixFailfast(A|B)$",
		"-test.failfast",
		"-test.v",
	)
	cmd.Env = processRetryScenarioEnvironment(
		processRetryAttemptToFixEnv+"=true",
		constants.CIVisibilityTestManagementAttemptToFixRetriesEnvironmentVariable+"=3",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("deferred A2F failfast subprocess unexpectedly passed:\n%s", output)
	}
	if !bytes.Contains(output, []byte("--- FAIL: TestDeferredProcessRetryAttemptToFixFailfastA")) {
		t.Fatalf("deferred A2F did not commit the irreversible first failure to the native test:\n%s", output)
	}
	if bytes.Contains(output, []byte("deferred-a2f-failfast-b-ran")) {
		t.Fatalf("native failfast allowed the following first attempt to run:\n%s", output)
	}
	if bytes.Contains(output, []byte("deferred-a2f-failfast-child-ran")) {
		t.Fatalf("native failfast allowed the queued A2F child to run:\n%s", output)
	}
}

func TestDeferredProcessRetryAttemptToFixFailfastA(t *testing.T) {
	if processRetryFixtureEnv(processRetryAttemptToFixEnv) != "true" {
		t.Skip("deferred A2F failfast fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		fmt.Println("deferred-a2f-failfast-child-ran")
		return
	}
	t.Fail()
}

func TestDeferredProcessRetryAttemptToFixFailfastB(t *testing.T) {
	if processRetryFixtureEnv(processRetryAttemptToFixEnv) != "true" {
		t.Skip("deferred A2F failfast fixture runs only from its controller subprocess")
	}
	fmt.Println("deferred-a2f-failfast-b-ran")
}

func TestDeferredProcessRetryFTRFailfastController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "deferred FTR failfast")
	path := filepath.Join(t.TempDir(), "attempts")
	cmd := exec.Command(
		os.Args[0],
		"-test.run=^TestDeferredProcessRetryFTRFailfast(A|B)$",
		"-test.failfast",
		"-test.v",
	)
	cmd.Env = processRetryScenarioEnvironment(processRetryDeferredFTRFailfastPathEnv + "=" + path)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("deferred FTR failfast subprocess unexpectedly passed:\n%s", output)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read deferred FTR failfast attempts: %v", readErr)
	}
	entries := strings.Fields(string(data))
	want := []string{"A:first", "B:first", "A:retry"}
	if !slices.Equal(entries, want) {
		t.Fatalf("deferred FTR failfast attempts = %v, want %v (output: %s)", entries, want, output)
	}
}

func TestDeferredProcessRetryFTRFailfastA(t *testing.T) {
	runDeferredProcessRetryFTRFailfastFixture(t, "A")
}

func TestDeferredProcessRetryFTRFailfastB(t *testing.T) {
	runDeferredProcessRetryFTRFailfastFixture(t, "B")
}

func runDeferredProcessRetryFTRFailfastFixture(t *testing.T, name string) {
	t.Helper()
	path := processRetryFixtureEnv(processRetryDeferredFTRFailfastPathEnv)
	if path == "" {
		t.Skip("deferred FTR failfast fixture runs only from its controller subprocess")
	}
	phase := "first"
	if processRetryFixtureChild() {
		phase = "retry"
	}
	appendStartupFixtureLine(path, name+":"+phase)
	t.Fail()
}

func TestProcessRetryCoverageUsesFirstParentAttempt(t *testing.T) {
	if !processRetryFixtureScenarioEnabled() && !processRetryFixtureChild() {
		if testing.CoverMode() == "" {
			t.Skip("coverage process-retry fixture runs only with Go coverage enabled")
		}
		coveragePath := filepath.Join(t.TempDir(), "first-attempt.out")
		cmd := exec.Command(
			os.Args[0],
			"-test.run=^TestProcessRetryCoverageUsesFirstParentAttempt$",
			"-test.coverprofile="+coveragePath,
			"-test.v",
		)
		cmd.Env = processRetryScenarioEnvironment()
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("coverage process-retry subprocess failed: %v\n%s", err, output)
		}
		coverage, err := os.ReadFile(coveragePath)
		if err != nil {
			t.Fatalf("read first-attempt coverage profile: %v", err)
		}
		parentFile, parentLine := processRetryCoverageParentMarker()
		childFile, childLine := processRetryCoverageChildMarker()
		if count, ok := processRetryCoverageCountForLine(coverage, parentFile, parentLine); !ok || count == 0 {
			t.Fatalf("parent-only coverage block count = %d, found = %t; want a positive count", count, ok)
		}
		if count, ok := processRetryCoverageCountForLine(coverage, childFile, childLine); !ok || count != 0 {
			t.Fatalf("child-only coverage block count = %d, found = %t; want zero", count, ok)
		}
		return
	}
	if processRetryFixtureChild() {
		for _, arg := range os.Args[1:] {
			if strings.HasPrefix(arg, "-test.coverprofile") || strings.HasPrefix(arg, "-test.gocoverdir") {
				t.Fatalf("coverage output flag leaked into retry child argv: %q", arg)
			}
		}
		if value, inherited := os.LookupEnv("GOCOVERDIR"); inherited {
			t.Fatalf("coverage output environment leaked into retry child: GOCOVERDIR=%q", value)
		}
		if coverageFirstAttemptRuns.Load() != 0 {
			t.Fatalf("coverage retry child inherited parent run count: %d", coverageFirstAttemptRuns.Load())
		}
		processRetryCoverageChildMarker()
		return
	}
	processRetryCoverageParentMarker()
	if coverageFirstAttemptRuns.Add(1) == 1 {
		t.Fatal("first coverage execution must fail and retry in a child process")
	}
	t.Fatal("coverage retry ran in the parent process")
}

func processRetryCoverageCountForLine(profile []byte, sourceFile string, sourceLine int) (int64, bool) {
	var total int64
	found := false
	for line := range strings.SplitSeq(string(profile), "\n") {
		matches := processRetryCoverageProfileBlock.FindStringSubmatch(line)
		if len(matches) != 5 || filepath.Base(matches[1]) != filepath.Base(sourceFile) {
			continue
		}
		startLine, startErr := strconv.Atoi(matches[2])
		endLine, endErr := strconv.Atoi(matches[3])
		count, countErr := strconv.ParseInt(matches[4], 10, 64)
		if startErr != nil || endErr != nil || countErr != nil || sourceLine < startLine || sourceLine > endLine {
			continue
		}
		found = true
		total += count
	}
	return total, found
}

func TestProcessRetryParallelEFDController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "parallel EFD")
	coordinationDir := t.TempDir()
	runProcessRetryFixtureSubprocess(t, "parallel-efd", []string{
		"-test.run=^TestProcessRetryParallelEFDParent$",
		"-test.v",
	},
		processRetryParallelEFDEnv+"=true",
		processRetryParallelEFDCoordinationDirEnv+"="+coordinationDir,
		constants.CIVisibilityInternalParallelEarlyFlakeDetectionEnabled+"=true",
		constants.CIVisibilityRetryProcessMaxConcurrencyEnvironmentVariable+"=2",
	)
}

func TestProcessRetryParallelEFDParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryParallelEFDEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("parallel EFD fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if run := parallelEFDRuns.Add(1); run != 1 {
			t.Fatalf("parallel EFD child executed the selected attempt %d times", run)
		}
		attempt, ok := integrations.LookupProcessRetryChildTransport(constants.CIVisibilityInternalRetryProcessAttempt)
		if !ok || attempt == "" {
			t.Fatal("parallel EFD child is missing its retry attempt")
		}
		coordinationDir := processRetryFixtureEnv(processRetryParallelEFDCoordinationDirEnv)
		if coordinationDir == "" {
			t.Fatal("parallel EFD child is missing its coordination directory")
		}
		if err := os.WriteFile(filepath.Join(coordinationDir, "ready-"+attempt), []byte(attempt), 0o600); err != nil {
			t.Fatalf("publish parallel EFD child readiness: %v", err)
		}
		deadline := time.Now().Add(10 * time.Second)
		delay := 10 * time.Millisecond
		for {
			entries, err := os.ReadDir(coordinationDir)
			if err != nil {
				t.Fatalf("read parallel EFD coordination directory: %v", err)
			}
			ready := 0
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "ready-") {
					ready++
				}
			}
			if ready >= 2 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("parallel EFD child %s did not overlap another retry child", attempt)
			}
			time.Sleep(delay)
			delay = nextProcessRetryFixturePollDelay(delay)
		}
	}
	if parallelEFDRuns.Add(1) == 1 {
		t.Fatal("first parallel EFD execution must fail to trigger process retries")
	}
	t.Fatal("parallel EFD retry ran in the parent process")
}

func TestProcessRetryRunSelectorController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "selector")
	runProcessRetryFixtureSubprocess(t, "run-selector", []string{
		"-test.run=^(TestProcessRetryRunSelectorParent|Other/Name)$/(OnlyThisSubtest)", "-test.v",
	}, processRetrySelectorFixtureEnv+"=true")
}

func TestProcessRetrySkipSelectorController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "selector")
	runProcessRetryFixtureSubprocess(t, "skip-selector", []string{
		"-test.run=^TestProcessRetrySkipSelectorParent$",
		"-test.skip=^TestProcessRetrySkipSelectorParent/SkippedSubtest$",
		"-test.v",
	}, processRetrySelectorFixtureEnv+"=true")
}

func TestProcessRetryProcessExitController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "process-exit")
	runProcessRetryFixtureSubprocess(t, "process-exit", []string{"-test.run=^TestProcessRetryProcessExitParent$", "-test.v"}, processRetryProcessExitFixtureEnv+"=true")
}

func TestProcessRetryMalformedJSONController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "malformed-json")
	runProcessRetryFixtureSubprocess(t, "malformed-json", []string{"-test.run=^TestProcessRetryMalformedJSONParent$", "-test.v"}, processRetryMalformedJSONFixtureEnv+"=true")
}

func TestProcessRetryTimeoutController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "timeout")
	runProcessRetryTimeoutFixtureSubprocess(t, "timeout", []string{"-test.run=^TestProcessRetryTimeoutParent$", "-test.v"},
		processRetryTimeoutFixtureEnv+"=true",
	)
}

func TestProcessRetryOutputTimeoutController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "output-timeout")
	runProcessRetryTimeoutFixtureSubprocess(t, "output-timeout", []string{"-test.run=^TestProcessRetryOutputTimeoutParent$", "-test.v"},
		processRetryOutputTimeoutFixtureEnv+"=true",
	)
}

func TestProcessRetryDescendantCleanupController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "descendant-cleanup")
	livenessPath := filepath.Join(t.TempDir(), "descendant-liveness")
	independentLivenessPath := filepath.Join(t.TempDir(), "descendant-independent-liveness")
	args := []string{"-test.run=^TestProcessRetryDescendantCleanupParent$", "-test.v"}
	env := []string{
		processRetryDescendantCleanupFixtureEnv + "=true",
		processRetryDescendantLivenessPathEnv + "=" + livenessPath,
		processRetryDescendantIndependentPathEnv + "=" + independentLivenessPath,
	}
	runProcessRetryFixtureSubprocess(t, "descendant-cleanup", args, env...)
	for _, path := range []string{livenessPath, independentLivenessPath} {
		address := processRetryDescendantAddress(t, path)
		waitForProcessRetryDescendantListenerClosed(t, address)
		assertProcessRetryDescendantDidNotExpireNaturally(t, path)
	}
}

func processRetryDescendantExpirationPath(livenessPath string) string {
	return livenessPath + ".expired"
}

func assertProcessRetryDescendantDidNotExpireNaturally(t *testing.T, livenessPath string) {
	t.Helper()
	_, err := os.Stat(processRetryDescendantExpirationPath(livenessPath))
	if err == nil {
		t.Fatal("process retry waited for a descendant helper to expire naturally")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("inspect process retry descendant expiration marker: %v", err)
	}
}

func processRetryDescendantAddress(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	delay := 10 * time.Millisecond
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			address := string(data)
			if _, _, err := net.SplitHostPort(address); err == nil {
				return address
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("process retry descendant helper did not publish a valid listener address: %v", err)
		}
		time.Sleep(delay)
		delay = nextProcessRetryFixturePollDelay(delay)
	}
}

func waitForProcessRetryDescendantListenerClosed(t *testing.T, address string) {
	t.Helper()
	const stableFailuresRequired = 3
	consecutiveFailures := 0
	deadline := time.Now().Add(10 * time.Second)
	delay := 10 * time.Millisecond
	for {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err != nil {
			consecutiveFailures++
			if consecutiveFailures >= stableFailuresRequired {
				return
			}
		} else {
			consecutiveFailures = 0
			delay = 10 * time.Millisecond
			_ = conn.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("process retry descendant helper survived cleanup: listener %s did not remain closed", address)
		}
		time.Sleep(delay)
		delay = nextProcessRetryFixturePollDelay(delay)
	}
}

func TestProcessRetryTransportIsolationController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "transport-isolation")
	runProcessRetryFixtureSubprocess(t, "transport-isolation", []string{"-test.run=^TestProcessRetryTransportIsolationParent$", "-test.v"}, processRetryTransportIsolationEnv+"=true")
}

func runProcessRetryFixtureSubprocess(t *testing.T, name string, args []string, environment ...string) []byte {
	t.Helper()
	output, err := executeProcessRetryFixtureSubprocess(args, environment...)
	if err != nil {
		t.Fatalf("%s process retry fixture failed: %v\n%s", name, err, output)
	}
	return output
}

func runProcessRetryTimeoutFixtureSubprocess(t *testing.T, name string, args []string, environment ...string) []byte {
	t.Helper()
	readyDir := t.TempDir()
	var output []byte
	var err error
	for attempt, timeout := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		readyPath := filepath.Join(readyDir, fmt.Sprintf("child-ready-%d", attempt))
		attemptEnvironment := append([]string(nil), environment...)
		attemptEnvironment = append(attemptEnvironment,
			processRetryTimeoutReadyPathEnv+"="+readyPath,
			constants.CIVisibilityRetryProcessTimeoutEnvironmentVariable+"="+timeout.String(),
		)
		output, err = executeProcessRetryFixtureSubprocess(args, attemptEnvironment...)
		if err == nil {
			return output
		}
		// Once the child body is ready, a missing sentinel is a capture regression,
		// not a slow-start condition that a longer timeout should hide.
		if _, statErr := os.Stat(readyPath); statErr == nil {
			break
		} else if !os.IsNotExist(statErr) {
			t.Fatalf("%s process retry fixture could not inspect child readiness: %v", name, statErr)
		}
	}
	t.Fatalf("%s process retry fixture failed: %v\n%s", name, err, output)
	return nil
}

func executeProcessRetryFixtureSubprocess(args []string, environment ...string) ([]byte, error) {
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = processRetryScenarioEnvironment(environment...)
	return cmd.CombinedOutput()
}

func nextProcessRetryFixturePollDelay(delay time.Duration) time.Duration {
	const maxDelay = 250 * time.Millisecond
	delay *= 2
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func TestProcessRetryRunSelectorParent(t *testing.T) {
	if processRetryFixtureEnv(processRetrySelectorFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("selector fixture runs only from its controller subprocess")
	}
	t.Run("OnlyThisSubtest", func(t *testing.T) {
		if processRetryFixtureChild() {
			if runSelectorSubtestRuns.Load() != 0 {
				t.Fatalf("process retry child inherited parent run selector count: %d", runSelectorSubtestRuns.Load())
			}
			return
		}
		if runSelectorSubtestRuns.Add(1) == 1 {
			t.Fatal("first run-selector execution must fail to trigger process retry")
		}
		t.Fatalf("run-selector retry ran in the parent process with run count %d", runSelectorSubtestRuns.Load())
	})
	t.Run("SiblingSubtest", func(t *testing.T) {
		t.Fatal("sibling subtest ran despite parent -run tail selector")
	})
}

func TestProcessRetrySkipSelectorParent(t *testing.T) {
	if processRetryFixtureEnv(processRetrySelectorFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("selector fixture runs only from its controller subprocess")
	}
	t.Run("ExecutedSubtest", func(t *testing.T) {
		if processRetryFixtureChild() {
			if skipSelectorSubtestRuns.Load() != 0 {
				t.Fatalf("process retry child inherited parent skip selector count: %d", skipSelectorSubtestRuns.Load())
			}
			return
		}
		if skipSelectorSubtestRuns.Add(1) == 1 {
			t.Fatal("first skip-selector execution must fail to trigger process retry")
		}
		t.Fatalf("skip-selector retry ran in the parent process with run count %d", skipSelectorSubtestRuns.Load())
	})
	t.Run("SkippedSubtest", func(t *testing.T) {
		t.Fatal("subtest ran despite parent -skip selector")
	})
}

func TestProcessRetryProcessExitParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryProcessExitFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("process-exit fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if processExitRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent process-exit count: %d", processExitRuns.Load())
		}
		fmt.Println(processRetryProcessExitLogSentinel)
		return
	}
	if processExitRuns.Add(1) == 1 {
		t.Fatal("first process-exit execution must fail to trigger process retry")
	}
	t.Fatalf("process-exit retry ran in the parent process with run count %d", processExitRuns.Load())
}

func TestProcessRetryMalformedJSONParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryMalformedJSONFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("malformed-json fixture runs only from its controller subprocess")
	}
	if malformedJSONRuns.Add(1) == 1 {
		t.Fatal("first malformed-json execution must fail to trigger process retry")
	}
	t.Fatalf("malformed-json retry ran in the parent process with run count %d", malformedJSONRuns.Load())
}

func TestProcessRetryTimeoutParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryTimeoutFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("timeout fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if timeoutRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent timeout count: %d", timeoutRuns.Load())
		}
		fmt.Println(processRetryTimeoutLogSentinel)
		markProcessRetryTimeoutChildReady(t)
		time.Sleep(time.Hour)
		return
	}
	if timeoutRuns.Add(1) == 1 {
		t.Fatal("first timeout execution must fail to trigger process retry")
	}
	t.Fatalf("timeout retry ran in the parent process with run count %d", timeoutRuns.Load())
}

func TestProcessRetryOutputTimeoutParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryOutputTimeoutFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("output-timeout fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if outputTimeoutRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent output-timeout count: %d", outputTimeoutRuns.Load())
		}
		for i := range 2048 {
			fmt.Fprintf(os.Stdout, "%s stdout %04d\n", processRetryOutputTimeoutLogSentinel, i)
			fmt.Fprintf(os.Stderr, "%s stderr %04d\n", processRetryOutputTimeoutLogSentinel, i)
		}
		markProcessRetryTimeoutChildReady(t)
		time.Sleep(time.Hour)
		return
	}
	if outputTimeoutRuns.Add(1) == 1 {
		t.Fatal("first output-timeout execution must fail to trigger process retry")
	}
	t.Fatalf("output-timeout retry ran in the parent process with run count %d", outputTimeoutRuns.Load())
}

func markProcessRetryTimeoutChildReady(t *testing.T) {
	t.Helper()
	path := processRetryFixtureEnv(processRetryTimeoutReadyPathEnv)
	if path == "" {
		t.Fatal("process retry timeout child is missing its readiness path")
	}
	if err := os.WriteFile(path, []byte("ready"), 0o600); err != nil {
		t.Fatalf("publish process retry timeout child readiness: %v", err)
	}
}

func TestProcessRetryDescendantCleanupParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryDescendantCleanupFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("descendant-cleanup fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if descendantCleanupRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent descendant-cleanup count: %d", descendantCleanupRuns.Load())
		}
		startDescendant := func(path string, inheritOutput bool) {
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(),
				processRetryDescendantHelperEnv+"=true",
				processRetryDescendantLivenessPathEnv+"="+path,
			)
			if inheritOutput {
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
			}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start process retry descendant helper: %v", err)
			}
			address := processRetryDescendantAddress(t, path)
			conn, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
			if err != nil {
				t.Fatalf("connect to process retry descendant helper: %v", err)
			}
			if err := conn.Close(); err != nil {
				t.Fatalf("close process retry descendant helper connection: %v", err)
			}
			if err := cmd.Process.Release(); err != nil {
				t.Fatalf("release process retry descendant helper handle: %v", err)
			}
		}
		startDescendant(processRetryFixtureEnv(processRetryDescendantLivenessPathEnv), true)
		startDescendant(processRetryFixtureEnv(processRetryDescendantIndependentPathEnv), false)
		fmt.Println(processRetryDescendantLogSentinel)
		return
	}
	if descendantCleanupRuns.Add(1) == 1 {
		t.Fatal("first descendant-cleanup execution must fail to trigger process retry")
	}
	t.Fatalf("descendant-cleanup retry ran in the parent process with run count %d", descendantCleanupRuns.Load())
}

func TestProcessRetryTransportIsolationParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryTransportIsolationEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("transport-isolation fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		for _, key := range []string{
			constants.CIVisibilityInternalRetryProcessChild,
			constants.CIVisibilityInternalRetryProcessResultPath,
			constants.CIVisibilityInternalRetryProcessTestName,
			constants.CIVisibilityInternalRetryProcessAttempt,
			constants.CIVisibilityInternalRetryProcessReason,
		} {
			if _, inherited := os.LookupEnv(key); inherited {
				t.Fatalf("process retry transport key remained inheritable: %s", key)
			}
		}

		cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.v")
		cmd.Env = append(os.Environ(), processRetryTransportProbeEnv+"=true")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("process retry transport descendant failed: %v\n%s", err, output)
		}

		if err := os.Setenv(constants.CIVisibilityInternalRetryProcessChild, "false"); err != nil {
			t.Fatalf("mutate process retry child marker: %v", err)
		}
		t.Cleanup(func() { _ = os.Unsetenv(constants.CIVisibilityInternalRetryProcessChild) })
		session := integrations.CreateTestSession()
		if session.SessionID() != 0 {
			t.Fatal("process retry child mode changed after mutating the live environment")
		}
		return
	}
	if transportIsolationRuns.Add(1) == 1 {
		t.Fatal("first transport-isolation execution must fail to trigger process retry")
	}
	t.Fatalf("transport-isolation retry ran in the parent process with run count %d", transportIsolationRuns.Load())
}

func TestProcessRetryStartupRerunsController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "startup")
	path := filepathForStartupFixture(t, "startup-reruns")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryStartupRerunsParent$", "-test.v")
	cmd.Env = processRetryScenarioEnvironment(
		processRetryStartupFixtureEnv+"=true",
		processRetryStartupRerunFileEnv+"="+path,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("startup-rerun subprocess failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	if len(lines) != 2 || lines[0] != "init" || lines[1] != "init" {
		t.Fatalf("expected exactly one parent and one child package init event, got %q", lines)
	}
}

func TestProcessRetryStartupConflictController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "startup conflict")
	resourcePath := filepathForStartupFixture(t, "startup-conflict-resource")
	markerPath := filepathForStartupFixture(t, "startup-conflict-marker")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryStartupConflictParent$", "-test.v")
	cmd.Env = processRetryScenarioEnvironment(
		processRetryStartupFixtureEnv+"=true",
		processRetryStartupConflictFileEnv+"="+resourcePath,
		processRetryStartupConflictMarkerEnv+"="+markerPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("startup-conflict subprocess failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	if len(lines) != 1 || lines[0] != "child_conflict" {
		t.Fatalf("expected exactly one child conflict and no parent conflicts, got %q", lines)
	}
}

func TestProcessRetryTestMainBaselineController(t *testing.T) {
	skipProcessRetryFixtureChildLaunchIneligible(t, "TestMain startup baseline")
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, processRetryTestMainWorkDir), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRetryTestMainBaselineParent$", "-test.v")
	cmd.Dir = root
	cmd.Env = processRetryScenarioEnvironment(
		processRetryTestMainFixtureEnv+"=true",
		processRetryTestMainCwdEnv+"="+root,
		"DD_GIT_REPOSITORY_URL=https://github.com/DataDog/dd-trace-go.git",
		"DD_GIT_COMMIT_SHA=1234567890abcdef1234567890abcdef12345678",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("TestMain baseline subprocess failed: %v\n%s", err, output)
	}
}

func TestProcessRetryStartupRerunsParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryStartupFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("startup fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if startupRerunRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent startup count: %d", startupRerunRuns.Load())
		}
		return
	}
	if startupRerunRuns.Add(1) == 1 {
		t.Fatal("first startup-rerun execution must fail to trigger process retry")
	}
	t.Fatalf("startup-rerun retry ran in the parent process with run count %d", startupRerunRuns.Load())
}

func TestProcessRetryStartupConflictParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryStartupFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("startup fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if startupConflictRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent startup conflict count: %d", startupConflictRuns.Load())
		}
		return
	}
	if startupConflictRuns.Add(1) == 1 {
		t.Fatal("first startup-conflict execution must fail to trigger process retry")
	}
	t.Fatalf("startup-conflict retry ran in the parent process with run count %d", startupConflictRuns.Load())
}

func TestProcessRetryTestMainBaselineParent(t *testing.T) {
	if processRetryFixtureEnv(processRetryTestMainFixtureEnv) != "true" && !processRetryFixtureChild() {
		t.Skip("TestMain baseline fixture runs only from its controller subprocess")
	}
	if processRetryFixtureChild() {
		if testMainBaselineRuns.Load() != 0 {
			t.Fatalf("process retry child inherited parent TestMain baseline count: %d", testMainBaselineRuns.Load())
		}
		return
	}
	if testMainBaselineRuns.Add(1) == 1 {
		t.Fatal("first TestMain baseline execution must fail to trigger process retry")
	}
	t.Fatalf("TestMain baseline retry ran in the parent process with run count %d", testMainBaselineRuns.Load())
}

func filepathForStartupFixture(t *testing.T, name string) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), name+"-*")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendStartupFixtureLine(path, line string) {
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line + "\n")
}

func processRetryFixtureChild() bool {
	return integrations.IsProcessRetryChild()
}

func processRetryFixtureEnv(name string) string {
	value, _ := env.Lookup(name)
	return value
}

func processRetryFixtureCommitSHA() string {
	if sha := env.Get("GITHUB_SHA"); sha != "" {
		return sha
	}
	return "local"
}
