// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/immutable"
)

// outChannelSize specifies the size of the profile output channel.
const outChannelSize = 5

// customProfileLabelLimit is the maximum number of pprof labels which can
// be used as custom attributes in the profiler UI
const customProfileLabelLimit = 10

var (
	mu             sync.Mutex
	activeProfiler *profiler
	containerID    = internal.ContainerID() // replaced in tests
	entityID       = internal.EntityID()    // replaced in tests
)

// Start starts the profiler. If the profiler is already running, it will be
// stopped and restarted with the given options.
//
// It may return an error if an API key is not provided by means of the
// WithAPIKey option, or if a hostname is not found.
func Start(opts ...Option) error {
	mu.Lock()
	defer mu.Unlock()

	if activeProfiler != nil {
		activeProfiler.stop()
	}
	p, err := newProfiler(opts...)
	if err != nil {
		return err
	}
	activeProfiler = p
	activeProfiler.run()
	traceprof.SetProfilerEnabled(true)
	return nil
}

// Stop cancels any ongoing profiling or upload operations and returns after
// everything has been stopped.
func Stop() {
	mu.Lock()
	if activeProfiler != nil {
		activeProfiler.stop()
		activeProfiler = nil
		traceprof.SetProfilerEnabled(false)
	}
	mu.Unlock()
}

// profiler collects and sends preset profiles to the Datadog API at a given frequency
// using a given configuration.
type profiler struct {
	cfg             *config           // profile configuration
	out             chan batch        // upload queue
	uploadFunc      func(batch) error // defaults to (*profiler).upload; replaced in tests
	exit            chan struct{}     // exit signals the profiler to stop; it is closed after stopping
	stopOnce        sync.Once         // stopOnce ensures the profiler is stopped exactly once.
	wg              sync.WaitGroup    // wg waits for all goroutines to exit when stopping.
	met             *metrics          // metric collector state
	deltas          map[ProfileType]*fastDeltaProfiler
	seq             uint64         // seq is the value of the profile_seq tag
	pendingProfiles sync.WaitGroup // signal that profile collection is done, for stopping CPU profiling

	testHooks testHooks

	// lastTrace is the last time an execution trace was collected
	lastTrace time.Time
}

// testHooks are functions that are replaced during testing which would normally
// depend on accessing runtime state that is not needed/available for the test
type testHooks struct {
	startCPUProfile func(w io.Writer) error
	stopCPUProfile  func()
	lookupProfile   func(name string, w io.Writer, debug int) error
}

func (p *profiler) startCPUProfile(w io.Writer) error {
	if p.testHooks.startCPUProfile != nil {
		return p.testHooks.startCPUProfile(w)
	}
	return pprof.StartCPUProfile(w)
}

func (p *profiler) stopCPUProfile() {
	if p.testHooks.startCPUProfile != nil {
		p.testHooks.stopCPUProfile()
		return
	}
	pprof.StopCPUProfile()
}

func (p *profiler) lookupProfile(name string, w io.Writer, debug int) error {
	if p.testHooks.lookupProfile != nil {
		return p.testHooks.lookupProfile(name, w, debug)
	}
	prof := pprof.Lookup(name)
	if prof == nil {
		return errors.New("profile not found")
	}
	return prof.WriteTo(w, debug)
}

// newProfiler creates a new, unstarted profiler.
func newProfiler(opts ...Option) (*profiler, error) {
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		return nil, errors.New("profiling not supported in AWS Lambda runtimes")
	}
	cfg, err := defaultConfig()
	if err != nil {
		return nil, err
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if len(cfg.customProfilerLabels) > customProfileLabelLimit {
		cfg.customProfilerLabels = cfg.customProfilerLabels[:customProfileLabelLimit]
	}
	// TODO(fg) remove this after making expGoroutineWaitProfile public.
	if os.Getenv("DD_PROFILING_WAIT_PROFILE") != "" {
		cfg.addProfileType(expGoroutineWaitProfile)
	}
	// Agentless upload is disabled by default as of v1.30.0, but
	// WithAgentlessUpload can be used to enable it for testing and debugging.
	if cfg.agentless {
		if !isAPIKeyValid(cfg.apiKey) {
			return nil, errors.New("profiler.WithAgentlessUpload requires a valid API key. Use profiler.WithAPIKey or the DD_API_KEY env variable to set it")
		}
		// Always warn people against using this mode for now. All customers should
		// use agent based uploading at this point.
		log.Warn("profiler.WithAgentlessUpload is currently for internal usage only and not officially supported.")
		cfg.targetURL = cfg.apiURL
	} else {
		// Historically people could use an API Key to enable agentless uploading.
		// As of v1.30.0 customers the default behavior is to use agent based
		// uploading regardless of the presence of an API key. So if we see an API
		// key configured, we warn the customers that this is probably a
		// misconfiguration.
		if cfg.apiKey != "" {
			log.Warn("You are currently setting profiler.WithAPIKey or the DD_API_KEY env variable, but as of dd-trace-go v1.30.0 this value is getting ignored by the profiler. Please see the profiler.WithAPIKey go docs and verify that your integration is still working. If you can't remove DD_API_KEY from your environment, you can use WithAPIKey(\"\") to silence this warning.")
		}
		cfg.targetURL = cfg.agentURL
	}
	if cfg.hostname == "" {
		hostname, err := os.Hostname()
		if err != nil {
			if cfg.targetURL == cfg.apiURL {
				return nil, fmt.Errorf("could not obtain hostname: %v", err)
			}
			log.Warn("unable to look up hostname: %v", err)
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
		return nil, fmt.Errorf("invalid upload timeout, must be > 0: %s", cfg.uploadTimeout)
	}
	for pt := range cfg.types {
		if _, ok := profileTypes[pt]; !ok {
			return nil, fmt.Errorf("unknown profile type: %d", pt)
		}
	}
	if cfg.cpuDuration > cfg.period {
		cfg.cpuDuration = cfg.period
	}
	if cfg.logStartup {
		logStartup(cfg)
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

	p := profiler{
		cfg:    cfg,
		out:    make(chan batch, outChannelSize),
		exit:   make(chan struct{}),
		met:    newMetrics(),
		deltas: make(map[ProfileType]*fastDeltaProfiler),
	}
	for pt := range cfg.types {
		if d := profileTypes[pt].DeltaValues; len(d) > 0 {
			p.deltas[pt] = newFastDeltaProfiler(d...)
		}
	}
	p.uploadFunc = p.upload
	return &p, nil
}

// run runs the profiler.
func (p *profiler) run() {
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
	startTelemetry(p.cfg)
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		tick := time.NewTicker(p.cfg.period)
		defer tick.Stop()
		p.met.reset(now()) // collect baseline metrics at profiler start
		p.collect(tick.C)
	}()
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.send()
	}()
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

	for {
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

		completed = completed[:0]
		// We need to increment pendingProfiles for every non-CPU
		// profile _before_ entering the next loop so that we know CPU
		// profiling will not complete until every other profile is
		// finished (because p.pendingProfiles will have been
		// incremented to count every non-CPU profile before CPU
		// profiling starts)

		profileTypes := p.enabledProfileTypes()

		// Decide whether we should record an execution trace
		p.cfg.traceConfig.Refresh()
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
					log.Error("Error getting %s profile: %v; skipping.", t, err)
					tags := append(p.cfg.tags.Slice(), t.Tag())
					p.cfg.statsd.Count("datadog.profiling.go.collect_error", 1, tags, 1)
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
			return
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
		expGoroutineWaitProfile,
		MetricsProfile,
		executionTrace,
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
			return
		case bat := <-p.out:
			if err := p.outputDir(bat); err != nil {
				log.Error("Failed to output profile to dir: %v", err)
			}
			if err := p.uploadFunc(bat); err != nil {
				log.Error("Failed to upload profile: %v", err)
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
func (p *profiler) interruptibleSleep(d time.Duration) {
	select {
	case <-p.exit:
	case <-time.After(d):
	}
}

// stop stops the profiler.
func (p *profiler) stop() {
	p.stopOnce.Do(func() {
		close(p.exit)
	})
	p.wg.Wait()
	if p.cfg.logStartup {
		log.Info("Profiling stopped")
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
