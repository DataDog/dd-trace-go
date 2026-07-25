// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/traceprof"
	"github.com/DataDog/dd-trace-go/v2/profiler/internal/immutable"
)

// outChannelSize specifies the size of the profile output channel.
const outChannelSize = 5

// customProfileLabelLimit is the maximum number of pprof labels which can
// be used as custom attributes in the profiler UI
const customProfileLabelLimit = 10

var (
	mu              sync.Mutex
	activeProfiler  *profiler
	startRevision   uint64
	stopTransition  chan struct{}
	claimTransition chan struct{}
	containerID     atomic.Pointer[string]
	entityID        atomic.Pointer[string]

	// errProfilerStopped is a sentinel for suppressing errors if we are
	// about to stop the profiler
	errProfilerStopped = errors.New("profiler stopped")

	// testLookupProfile is a global hook for testing that replaces the
	// pprof.Lookup-based profile collection. Set it before calling Start
	// and restore it to nil after calling Stop.
	testLookupProfile func(name string, w io.Writer, debug int) error
)

func init() {
	cid := internal.ContainerID()
	containerID.Store(&cid)
	eid := internal.EntityID()
	entityID.Store(&eid)
}

// Start starts the profiler. If the profiler is already running, it will be
// stopped and restarted with the given options.
//
// It may return an error if an API key is not provided by means of the
// WithAPIKey option, or if a hostname is not found.
//
// If DD_PROFILING_ENABLED=false is set in the process environment, it will
// prevent the profiler from starting.
func Start(opts ...Option) error {
	revision, old, priorStop, priorClaims, stopDone := beginProfilerTransition()
	waitForProfilerTransition(priorStop)
	waitForProfilerTransition(priorClaims)
	if old != nil {
		old.stopRuntime()
	}
	close(stopDone)
	if old != nil {
		old.logStopped()
	}

	p, reports, err := prepareProfiler(opts...)
	if err != nil {
		reportUnpublishedProfilerAttempt(revision, reports)
		return err
	}
	if !p.cfg.enabled {
		reportUnpublishedProfilerAttempt(revision, reports)
		return nil
	}
	if !publishProfilerCandidate(revision, p, &reports) {
		return nil
	}
	reportStartedProfiler(p, reports)
	return nil
}

func beginProfilerTransition() (revision uint64, old *profiler, priorStop, priorClaims, done chan struct{}) {
	mu.Lock()
	startRevision++
	revision = startRevision
	old = activeProfiler
	activeProfiler = nil
	traceprof.SetProfilerEnabled(false)
	priorStop = stopTransition
	priorClaims = claimTransition
	done = make(chan struct{})
	stopTransition = done
	mu.Unlock()
	return revision, old, priorStop, priorClaims, done
}

func waitForProfilerTransition(done chan struct{}) {
	if done != nil {
		<-done
	}
}

func publishProfilerCandidate(revision uint64, p *profiler, reports *profilerStartReports) bool {
	mu.Lock()
	if startRevision != revision {
		mu.Unlock()
		return false
	}
	claimDone := make(chan struct{})
	claimTransition = claimDone
	mu.Unlock()

	releaseClaims, accepted, reportConflicts := internalconfig.PrepareProductClaims(
		internalconfig.ProductProfiler,
		p.cfg.requestedClaims(),
	)
	reports.reportConflicts = reportConflicts
	p.cfg.restoreRejectedClaims(accepted)
	finalizeProfilerConfig(p.cfg)

	mu.Lock()
	if startRevision != revision {
		mu.Unlock()
		releaseClaims()
		close(claimDone)
		return false
	}
	p.releaseClaims = releaseClaims
	activeProfiler = p
	activeProfiler.runRuntime()
	traceprof.SetProfilerEnabled(true)
	close(claimDone)
	mu.Unlock()
	return true
}

func reportStartedProfiler(p *profiler, reports profilerStartReports) {
	if !isActiveProfiler(p) {
		return
	}
	if p.cfg.logStartup {
		logStartup(p.cfg)
	}
	if !isActiveProfiler(p) {
		return
	}
	internalconfig.ReportProfilerConfigEvents(reports.configEvents)
	if !isActiveProfiler(p) {
		return
	}
	if reports.reportGit != nil {
		reports.reportGit()
	}
	if !isActiveProfiler(p) {
		return
	}
	if reports.reportConflicts != nil {
		reports.reportConflicts()
	}
	if !isActiveProfiler(p) {
		return
	}
	startTelemetry(p.cfg, func() bool { return isActiveProfiler(p) })
}

func reportUnpublishedProfilerAttempt(revision uint64, reports profilerStartReports) {
	if !isProfilerRevisionCurrent(revision) {
		return
	}
	internalconfig.ReportProfilerConfigEvents(reports.configEvents)
	if !isProfilerRevisionCurrent(revision) {
		return
	}
	if reports.reportGit != nil {
		reports.reportGit()
	}
}

func isActiveProfiler(p *profiler) bool {
	mu.Lock()
	defer mu.Unlock()
	return activeProfiler == p
}

func isProfilerRevisionCurrent(revision uint64) bool {
	mu.Lock()
	defer mu.Unlock()
	return startRevision == revision
}

// Stop cancels any ongoing profiling or upload operations and returns after
// everything has been stopped.
func Stop() {
	_, old, priorStop, priorClaims, stopDone := beginProfilerTransition()
	waitForProfilerTransition(priorStop)
	waitForProfilerTransition(priorClaims)
	if old != nil {
		old.stopRuntime()
	}
	close(stopDone)
	if old != nil {
		old.logStopped()
	}
}

// profiler collects and sends preset profiles to the Datadog API at a given frequency
// using a given configuration.
type profiler struct {
	cfg             *config        // profile configuration
	out             chan batch     // upload queue
	exit            chan struct{}  // exit signals the profiler to stop; it is closed after stopping
	stopOnce        sync.Once      // stopOnce ensures the profiler is stopped exactly once.
	wg              sync.WaitGroup // wg waits for all goroutines to exit when stopping.
	met             *metrics       // metric collector state
	deltas          map[ProfileType]*fastDeltaProfiler
	compressors     map[ProfileType]compressor
	seq             uint64         // seq is the value of the profile_seq tag
	pendingProfiles sync.WaitGroup // signal that profile collection is done, for stopping CPU profiling
	releaseClaims   func()         // release programmatic shared-configuration claims

	// lastTrace is the last time an execution trace was collected
	lastTrace time.Time
}

func (p *profiler) lookupProfile(name string, w io.Writer, debug int) error {
	if testLookupProfile != nil {
		return testLookupProfile(name, w, debug)
	}
	prof := pprof.Lookup(name)
	if prof == nil {
		return errors.New("profile not found")
	}
	return prof.WriteTo(w, debug)
}

var (
	errProfilingNotSupportedInAWSLambda = errors.New("profiling is not supported in AWS Lambda runtimes")
	errAgentlessUploadRequiresAPIKey    = errors.New("agentless upload requires a valid API key - set the DD_API_KEY env variable to configure one")
)

// newProfiler creates a new, unstarted profiler.
func newProfiler(opts ...Option) (*profiler, error) {
	p, reports, err := prepareProfiler(opts...)
	if err != nil {
		reports.reportResolved()
		return nil, err
	}
	releaseClaims, accepted, reportConflicts := internalconfig.PrepareProductClaims(
		internalconfig.ProductProfiler,
		p.cfg.requestedClaims(),
	)
	reports.reportConflicts = reportConflicts
	p.cfg.restoreRejectedClaims(accepted)
	finalizeProfilerConfig(p.cfg)
	p.releaseClaims = releaseClaims
	if p.cfg.logStartup {
		logStartup(p.cfg)
	}
	reports.reportResolved()
	return p, nil
}

func prepareProfiler(opts ...Option) (*profiler, profilerStartReports, error) {
	var reports profilerStartReports
	if env.Get("AWS_LAMBDA_FUNCTION_NAME") != "" {
		return nil, reports, errProfilingNotSupportedInAWSLambda
	}
	cfg, reports, err := prepareDefaultConfig()
	if err != nil {
		return nil, reports, err
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if len(cfg.customProfilerLabels) > customProfileLabelLimit {
		cfg.customProfilerLabels = cfg.customProfilerLabels[:customProfileLabelLimit]
	}

	if cfg.traceConfig.Enabled && (cfg.traceConfig.Period == 0 || cfg.traceConfig.Limit == 0) {
		log.Warn("Invalid execution trace config, enabled is true but size limit or frequency is 0. Disabling execution tracing")
		cfg.traceConfig.Enabled = false
	}

	// Unconditionally enable goroutine leak profiling if it's available.
	if goroutineLeakProfileAvailable() {
		cfg.addProfileType(goroutineLeakProfile)
	}
	// Agentless upload is disabled by default as of v1.30.0, but
	// DD_PROFILING_AGENTLESS can be set to enable it for testing and debugging.
	if cfg.agentless {
		if !isAPIKeyValid(cfg.apiKey) {
			return nil, reports, errAgentlessUploadRequiresAPIKey
		}
		// Always warn people against using this mode for now. All customers should
		// use agent based uploading at this point.
		log.Warn("Agentless upload is currently for internal usage only and not officially supported.")
	} else {
		// Historically people could use an API Key to enable agentless uploading.
		// As of v1.30.0 customers the default behavior is to use agent based
		// uploading regardless of the presence of an API key. So if we see an API
		// key configured, we warn the customers that this is probably a
		// misconfiguration.
		if cfg.apiKey != "" {
			log.Warn("You are currently setting the DD_API_KEY env variable, but as of dd-trace-go v1.30.0 this value is getting ignored by the profiler. Please verify that your integration is still working.")
		}
	}
	if cfg.hostname == "" {
		hostname, err := os.Hostname()
		if err != nil {
			if cfg.agentless {
				return nil, reports, fmt.Errorf("could not obtain hostname: %s", err)
			}
			log.Warn("unable to look up hostname: %s", err.Error())
		}
		cfg.hostname = hostname
	}
	// uploadTimeout defaults to DefaultUploadTimeout, but in theory a user might
	// set it to 0 or a negative value. However, it's not clear what this should
	// mean, and most meanings we could assign seem to be bad: Not having a
	// timeout is dangerous, having a timeout that fires immediately breaks
	// uploading, and silently defaulting to the default timeout is confusing.
	// So let's just stay clear of all of this by not allowing such values.
	//
	// see similar discussion: https://github.com/golang/go/issues/39177
	if cfg.uploadTimeout <= 0 {
		return nil, reports, fmt.Errorf("invalid upload timeout, must be > 0: %s", cfg.uploadTimeout)
	}
	for pt := range cfg.types {
		if _, ok := profileTypes[pt]; !ok {
			return nil, reports, fmt.Errorf("unknown profile type: %d", pt)
		}
	}
	if cfg.cpuDuration > cfg.period {
		cfg.cpuDuration = cfg.period
	}
	p := profiler{
		cfg:         cfg,
		out:         make(chan batch, outChannelSize),
		exit:        make(chan struct{}),
		met:         newMetrics(),
		deltas:      make(map[ProfileType]*fastDeltaProfiler),
		compressors: make(map[ProfileType]compressor),
	}
	types := slices.Collect(maps.Keys(cfg.types))
	// We need to manually add executionTrace to the list of profile types to be
	// initialized for compression, because it's not part of the cfg.types map.
	// Instead it gets added dynamically in profiler.collect.
	if p.cfg.traceConfig.Enabled {
		types = append(types, executionTrace)
	}
	var pipelineBuilder compressionPipelineBuilder
	for _, pt := range types {
		isDelta := p.cfg.deltaProfiles && len(profileTypes[pt].DeltaValues) > 0
		in, out := compressionStrategy(pt, isDelta, p.cfg.compressionConfig)
		compressor, err := pipelineBuilder.Build(in, out)
		if err != nil {
			return nil, reports, err
		}
		p.compressors[pt] = compressor

		if isDelta {
			p.deltas[pt] = newFastDeltaProfiler(compressor, profileTypes[pt].DeltaValues...)
		}
	}
	return &p, reports, nil
}

func finalizeProfilerConfig(cfg *config) {
	if cfg.agentless {
		cfg.targetURL = cfg.apiURL
	} else {
		cfg.targetURL = cfg.agentURL
	}
	var tags []string
	var seenVersionTag bool
	for _, tag := range cfg.tags.Slice() {
		// If the user configured a tag via DD_VERSION or WithVersion,
		// override any version tags the user provided via WithTags,
		// since having more than one version tag breaks the comparison
		// UI. If a version is only supplied by WithTags, keep only the
		// first one.
		if strings.HasPrefix(strings.ToLower(tag), "version:") {
			if cfg.version != "" || seenVersionTag {
				continue
			}
			seenVersionTag = true
		}
		tags = append(tags, tag)
	}
	if cfg.version != "" {
		tags = append(tags, "version:"+cfg.version)
	}
	cfg.tags = immutable.NewStringSlice(tags)
}

var goroutineLeakProfileAvailable = sync.OnceValue(func() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, s := range info.Settings {
		if s.Key != "GOEXPERIMENT" {
			continue
		}
		if strings.Contains(s.Value, "goroutineleakprofile") {
			return true
		}
	}
	return false
})

// runRuntime publishes the profiler's runtime effects and goroutines.
func (p *profiler) runRuntime() {
	profileEnabled := func(t ProfileType) bool {
		_, ok := p.cfg.types[t]
		return ok
	}
	if profileEnabled(MutexProfile) {
		runtime.SetMutexProfileFraction(p.cfg.mutexFraction)
	}
	if profileEnabled(BlockProfile) {
		runtime.SetBlockProfileRate(p.cfg.blockRate)
	}
	p.wg.Go(func() {
		tick := time.NewTicker(p.cfg.period)
		defer tick.Stop()
		p.met.reset(now()) // collect baseline metrics at profiler start
		p.collect(tick.C)
	})
	p.wg.Go(func() {
		p.send()
	})
}

// collect runs the profile types found in the configuration whenever the ticker receives
// an item.
func (p *profiler) collect(ticker <-chan time.Time) {
	defer close(p.out)
	var (
		// mu guards completed
		mu        sync.Mutex
		completed []*profile
		wg        sync.WaitGroup
	)

	// Enable endpoint counting (if configured). This causes some minimal
	// overhead to the tracer, see BenchmarkEndpointCounter.
	endpointCounter := traceprof.GlobalEndpointCounter()
	endpointCounter.SetEnabled(p.cfg.endpointCountEnabled)
	// Disable and reset when func returns (profiler stopped) to remove tracer
	// overhead, free up the counter map, and avoid it from growing again.
	defer func() {
		endpointCounter.SetEnabled(false)
		endpointCounter.GetAndReset()
	}()

	exit := false
	for !exit {
		bat := batch{
			seq:   p.seq,
			host:  p.cfg.hostname,
			start: now(),
			extraTags: []string{
				// _dd.profiler.go_execution_trace_enabled indicates whether execution
				// tracing is enabled, to distinguish between missing a trace
				// because we don't collect them every profiling cycle from
				// missing a trace because the feature isn't turned on.
				fmt.Sprintf("_dd.profiler.go_execution_trace_enabled:%v", p.cfg.traceConfig.Enabled),
				pgoTag(),
			},
			customAttributes: p.cfg.customProfilerLabels,
		}
		p.seq++

		clear(completed)
		completed = completed[:0]
		// We need to increment pendingProfiles for every non-CPU
		// profile _before_ entering the next loop so that we know CPU
		// profiling will not complete until every other profile is
		// finished (because p.pendingProfiles will have been
		// incremented to count every non-CPU profile before CPU
		// profiling starts)

		profileTypes := p.enabledProfileTypes()

		// Decide whether we should record an execution trace.
		// Randomly record a trace with probability (profile period) / (trace period).
		// Note that if the trace period is equal to or less than the profile period,
		// we will always record a trace
		// We do multiplication here instead of division to defensively guard against
		// division by 0
		shouldTraceRandomly := rand.Float64()*float64(p.cfg.traceConfig.Period) < float64(p.cfg.period)
		// As a special case, we want to trace during the first
		// profiling cycle since startup activity is generally much
		// different than regular operation
		firstCycle := bat.seq == 0
		shouldTrace := p.cfg.traceConfig.Enabled && (shouldTraceRandomly || firstCycle)
		if shouldTrace {
			profileTypes = append(profileTypes, executionTrace)
		}

		for _, t := range profileTypes {
			if t != CPUProfile {
				p.pendingProfiles.Add(1)
			}
		}
		for _, t := range profileTypes {
			wg.Add(1)
			go func(t ProfileType) {
				defer wg.Done()
				if t != CPUProfile {
					defer p.pendingProfiles.Done()
				}
				profs, err := p.runProfile(t)
				if err != nil {
					if err != errProfilerStopped {
						log.Error("Error getting %s profile: %v; skipping.", t, err.Error())
						tags := append(p.cfg.tags.Slice(), t.Tag())
						p.cfg.statsd.Count("datadog.profiling.go.collect_error", 1, tags, 1)
					}
					return
				}
				mu.Lock()
				defer mu.Unlock()
				completed = append(completed, profs...)
			}(t)
		}
		wg.Wait()
		for _, prof := range completed {
			if prof.pt == executionTrace {
				// If the profile batch includes a runtime execution trace, add a tag so
				// that the uploads are more easily discoverable in the UI.
				bat.extraTags = append(bat.extraTags, "go_execution_traced:yes")
			}
			bat.addProfile(prof)
		}

		// Wait until the next profiling period starts or the profiler is stopped.
		select {
		case <-ticker:
			// Usually ticker triggers right away because the non-CPU profiles cause
			// the wg.Wait above to sleep until the end of the profiling period.
			// Edge case: If only the CPU profile is enabled, and the cpu duration is
			// is less than the configured profiling period, the ticker will block
			// until the end of the profiling period.
		case <-p.exit:
			if !p.cfg.flushOnExit {
				return
			}
			// If we're flushing, we enqueue the batch before exiting the loop.
			exit = true
		}

		// Include endpoint hits from tracer in profile `event.json`.
		// Also reset the counters for the next profile period.
		bat.endpointCounts = endpointCounter.GetAndReset()
		// Record the end time of the profile.
		// This is used by the backend to upscale the endpoint counts if the cpu
		// duration is less than the profile duration. The formula is:
		//
		// factor = (end - start) / cpuDuration
		// counts = counts * factor
		//
		// The default configuration of the profiler (cpu duration = profiling
		// period) results in a factor of 1.
		bat.end = time.Now()
		// Upload profiling data.
		p.enqueueUpload(bat)
	}
}

// enabledProfileTypes returns the enabled profile types in a deterministic
// order. The CPU profile always comes first because people might spot
// interesting events in there and then try to look for the counter-part event
// in the mutex/heap/block profile. Deterministic ordering is also important
// for delta profiles, otherwise they'd cover varying profiling periods.
func (p *profiler) enabledProfileTypes() []ProfileType {
	order := []ProfileType{
		CPUProfile,
		HeapProfile,
		BlockProfile,
		MutexProfile,
		GoroutineProfile,
		MetricsProfile,
		executionTrace,
		goroutineLeakProfile,
	}
	enabled := []ProfileType{}
	for _, t := range order {
		if _, ok := p.cfg.types[t]; ok {
			enabled = append(enabled, t)
		}
	}
	return enabled
}

// enqueueUpload pushes a batch of profiles onto the queue to be uploaded. If there is no room, it will
// evict the oldest profile to make some. Typically a batch would be one of each enabled profile.
func (p *profiler) enqueueUpload(bat batch) {
	for {
		select {
		case p.out <- bat:
			return // 👍
		default:
			// queue is full; evict oldest
			select {
			case <-p.out:
				p.cfg.statsd.Count("datadog.profiling.go.queue_full", 1, p.cfg.tags.Slice(), 1)
				log.Warn("Evicting one profile batch from the upload queue to make room.")
			default:
				// this case should be almost impossible to trigger, it would require a
				// full p.out to completely drain within nanoseconds or extreme
				// scheduling decisions by the runtime.
			}
		}
	}
}

// send takes profiles from the output queue and uploads them.
func (p *profiler) send() {
	for {
		select {
		case <-p.exit:
			if !p.cfg.flushOnExit {
				return
			}
		case bat, ok := <-p.out:
			if !ok {
				return
			}
			if err := p.outputDir(bat); err != nil {
				log.Error("Failed to output profile to dir: %s", err.Error())
			}
			if err := p.upload(bat); err != nil {
				log.Error("Failed to upload profile: %s", err.Error())
			}
		}
	}
}

func (p *profiler) outputDir(bat batch) error {
	if p.cfg.outputDir == "" {
		return nil
	}
	// Basic ISO 8601 Format in UTC as the name for the directories.
	dir := bat.end.UTC().Format("20060102T150405Z")
	dirPath := filepath.Join(p.cfg.outputDir, dir)
	// 0755 is what mkdir does, should be reasonable for the use cases here.
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return err
	}

	for _, prof := range bat.profiles {
		filePath := filepath.Join(dirPath, prof.name)
		// 0644 is what touch does, should be reasonable for the use cases here.
		if err := os.WriteFile(filePath, prof.data, 0644); err != nil {
			return err
		}
	}
	return nil
}

// interruptibleSleep sleeps for the given duration or until interrupted by the
// p.exit channel being closed.
// Returns whether the sleep was interrupted
func (p *profiler) interruptibleSleep(d time.Duration) bool {
	select {
	case <-p.exit:
		return true
	case <-time.After(d):
		return false
	}
}

// stopRuntime stops collection and releases product claims without invoking
// user-controlled callbacks.
func (p *profiler) stopRuntime() {
	p.stopOnce.Do(func() {
		close(p.exit)
	})
	p.wg.Wait()
	p.releaseProductClaims()
}

func (p *profiler) logStopped() {
	if p.cfg.logStartup {
		log.Info("Profiling stopped")
	}
}

func (p *profiler) releaseProductClaims() {
	if p.releaseClaims != nil {
		p.releaseClaims()
	}
}

// StatsdClient implementations can count and time certain event occurrences that happen
// in the profiler.
type StatsdClient interface {
	// Count counts how many times an event happened, at the given rate using the given tags.
	Count(event string, times int64, tags []string, rate float64) error
	// Timing creates a histogram metric of the values registered as the duration of a certain event.
	Timing(event string, duration time.Duration, tags []string, rate float64) error
}
