// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"runtime"
	"runtime/trace"
	"time"

	"github.com/DataDog/gostackparse"
	pprofile "github.com/google/pprof/profile"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/fastdelta"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/pprofutils"
)

// ProfileType represents a type of profile that the profiler is able to run.
type ProfileType int

const (
	// HeapProfile reports memory allocation samples; used to monitor current
	// and historical memory usage, and to check for memory leaks.
	HeapProfile ProfileType = iota
	// CPUProfile determines where a program spends its time while actively consuming
	// CPU cycles (as opposed to while sleeping or waiting for I/O).
	CPUProfile
	// BlockProfile shows where goroutines block waiting on mutex and channel
	// operations. The block profile is not enabled by default and may cause
	// noticeable CPU overhead. We recommend against enabling it, see
	// DefaultBlockRate for more information.
	BlockProfile
	// MutexProfile reports the lock contentions. When you think your CPU is not fully utilized due
	// to a mutex contention, use this profile. Mutex profile is not enabled by default.
	MutexProfile
	// GoroutineProfile reports stack traces of all current goroutines
	GoroutineProfile
	// expGoroutineWaitProfile reports stack traces and wait durations for
	// goroutines that have been waiting or blocked by a syscall for > 1 minute
	// since the last GC. This feature is currently experimental and only
	// available within DD by setting the DD_PROFILING_WAIT_PROFILE env variable.
	expGoroutineWaitProfile
	// MetricsProfile reports top-line metrics associated with user-specified profiles
	MetricsProfile

	// executionTrace is the runtime/trace execution tracer.
	// This is private, as this trace requires special explicit configuration and
	// shouldn't just be added to WithProfileTypes
	executionTrace
)

// profileType holds the implementation details of a ProfileType.
type profileType struct {
	// Type gets populated automatically by ProfileType.lookup().
	Type ProfileType
	// Name specifies the profile name as used with pprof.Lookup(name) (in
	// collectGenericProfile) and returned by ProfileType.String(). For profile
	// types that don't use this approach (e.g. CPU) the name isn't used for
	// anything.
	Name string
	// Filename is the filename used for uploading the profile to the datadog
	// backend which is aware of them. Delta profiles are prefixed with "delta-"
	// automatically. In theory this could be derrived from the Name field, but
	// this isn't done due to idiosyncratic filename used by the
	// GoroutineProfile.
	Filename string
	// Collect collects the given profile and returns the data for it. Most
	// profiles will be in pprof format, i.e. gzip compressed proto buf data.
	Collect func(p *profiler) ([]byte, error)
	// DeltaValues identifies which values in profile samples should be modified
	// when delta profiling is enabled. Empty DeltaValues means delta profiling is
	// not supported for this profile type
	DeltaValues []pprofutils.ValueType
}

// profileTypes maps every ProfileType to its implementation.
var profileTypes = map[ProfileType]profileType{
	CPUProfile: {
		Name:     "cpu",
		Filename: "cpu.pprof",
		Collect: func(p *profiler) ([]byte, error) {
			var buf bytes.Buffer
			// Start the CPU profiler at the end of the profiling
			// period so that we're sure to capture the CPU usage of
			// this library, which mostly happens at the end
			p.interruptibleSleep(p.cfg.period - p.cfg.cpuDuration)
			if p.cfg.cpuProfileRate != 0 {
				// The profile has to be set each time before
				// profiling is started. Otherwise,
				// runtime/pprof.StartCPUProfile will set the
				// rate itself.
				runtime.SetCPUProfileRate(p.cfg.cpuProfileRate)
			}

			if err := p.startCPUProfile(&buf); err != nil {
				return nil, err
			}
			p.interruptibleSleep(p.cfg.cpuDuration)

			// We want the CPU profiler to finish last so that it can
			// properly record all of our profile processing work for
			// the other profile types
			p.pendingProfiles.Wait()
			p.stopCPUProfile()
			return buf.Bytes(), nil
		},
	},
	// HeapProfile is complex due to how the Go runtime exposes it. It contains 4
	// sample types alloc_objects/count, alloc_space/bytes, inuse_objects/count,
	// inuse_space/bytes. The first two represent allocations over the lifetime
	// of the process, so we do delta profiling for them. The last two are
	// snapshots of the current heap state, so we leave them as-is.
	HeapProfile: {
		Name:     "heap",
		Filename: "heap.pprof",
		Collect:  collectGenericProfile("heap", HeapProfile),
		DeltaValues: []pprofutils.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
		},
	},
	MutexProfile: {
		Name:     "mutex",
		Filename: "mutex.pprof",
		Collect:  collectGenericProfile("mutex", MutexProfile),
		DeltaValues: []pprofutils.ValueType{
			{Type: "contentions", Unit: "count"},
			{Type: "delay", Unit: "nanoseconds"},
		},
	},
	BlockProfile: {
		Name:     "block",
		Filename: "block.pprof",
		Collect:  collectGenericProfile("block", BlockProfile),
		DeltaValues: []pprofutils.ValueType{
			{Type: "contentions", Unit: "count"},
			{Type: "delay", Unit: "nanoseconds"},
		},
	},
	GoroutineProfile: {
		Name:     "goroutine",
		Filename: "goroutines.pprof",
		Collect:  collectGenericProfile("goroutine", GoroutineProfile),
	},
	expGoroutineWaitProfile: {
		Name:     "goroutinewait",
		Filename: "goroutineswait.pprof",
		Collect: func(p *profiler) ([]byte, error) {
			if n := runtime.NumGoroutine(); n > p.cfg.maxGoroutinesWait {
				return nil, fmt.Errorf("skipping goroutines wait profile: %d goroutines exceeds DD_PROFILING_WAIT_PROFILE_MAX_GOROUTINES limit of %d", n, p.cfg.maxGoroutinesWait)
			}

			p.interruptibleSleep(p.cfg.period)

			var (
				now   = now()
				text  = &bytes.Buffer{}
				pprof = &bytes.Buffer{}
			)
			if err := p.lookupProfile("goroutine", text, 2); err != nil {
				return nil, err
			}
			err := goroutineDebug2ToPprof(text, pprof, now)
			return pprof.Bytes(), err
		},
	},
	MetricsProfile: {
		Name:     "metrics",
		Filename: "metrics.json",
		Collect: func(p *profiler) ([]byte, error) {
			var buf bytes.Buffer
			p.interruptibleSleep(p.cfg.period)
			err := p.met.report(now(), &buf)
			return buf.Bytes(), err
		},
	},
	executionTrace: {
		Name:     "execution-trace",
		Filename: "go.trace",
		Collect: func(p *profiler) ([]byte, error) {
			if !p.shouldTrace() {
				return nil, errors.New("started tracing erroneously, indicating a bug in the profiler")
			}
			p.lastTrace = time.Now()
			buf := new(bytes.Buffer)
			lt := newLimitedTraceCollector(buf, int64(p.cfg.traceConfig.Limit))
			if err := trace.Start(lt); err != nil {
				return nil, err
			}
			select {
			case <-p.exit: // Profiling was stopped
			case <-time.After(p.cfg.period): // The profiling cycle has ended
			case <-lt.done: // The trace size limit was exceeded
			}
			trace.Stop()
			return buf.Bytes(), nil
		},
	},
}

// defaultExecutionTraceSizeLimit is the default upper bound, in bytes,
// of an executiont trace.
//
// 5MB was selected to give reasonable latency for processing, both online and
// using offline tools. This is a conservative estimate--we could possibly get
// away with 10MB and still have a tolerable experience.
const defaultExecutionTraceSizeLimit = 5 * 1024 * 1024

type limitedTraceCollector struct {
	w       io.Writer
	limit   int64
	written int64
	// done is closed to signal that the limit has been exceeded
	done chan struct{}
}

func newLimitedTraceCollector(w io.Writer, limit int64) *limitedTraceCollector {
	return &limitedTraceCollector{w: w, limit: limit, done: make(chan struct{})}
}

// Write calls the underlying writer's Write method, and stops tracing if the
// limit has been reached.
func (l *limitedTraceCollector) Write(p []byte) (n int, err error) {
	n, err = l.w.Write(p)
	if err != nil {
		// TODO: still count n against the limit?
		return
	}
	l.written += int64(n)
	if l.written >= l.limit {
		select {
		case <-l.done:
		default:
			close(l.done)
		}
	}
	return
}

func collectGenericProfile(name string, pt ProfileType) func(p *profiler) ([]byte, error) {
	return func(p *profiler) ([]byte, error) {
		p.interruptibleSleep(p.cfg.period)

		var buf bytes.Buffer
		err := p.lookupProfile(name, &buf, 0)
		data := buf.Bytes()
		dp, ok := p.deltas[pt]
		if !ok || !p.cfg.deltaProfiles {
			return data, err
		}

		start := time.Now()
		delta, err := dp.Delta(data)
		tags := append(p.cfg.tags.Slice(), fmt.Sprintf("profile_type:%s", name))
		p.cfg.statsd.Timing("datadog.profiling.go.delta_time", time.Since(start), tags, 1)
		if err != nil {
			return nil, fmt.Errorf("delta profile error: %s", err)
		}
		return delta, err
	}
}

// lookup returns t's profileType implementation.
func (t ProfileType) lookup() profileType {
	c, ok := profileTypes[t]
	if ok {
		c.Type = t
		return c
	}
	return profileType{
		Type:     t,
		Name:     "unknown",
		Filename: "unknown",
		Collect: func(_ *profiler) ([]byte, error) {
			return nil, errors.New("profile type not implemented")
		},
	}
}

// String returns the name of the profile.
func (t ProfileType) String() string {
	return t.lookup().Name
}

// Filename is the identifier used on upload.
func (t ProfileType) Filename() string {
	return t.lookup().Filename
}

// Tag used on profile metadata
func (t ProfileType) Tag() string {
	return fmt.Sprintf("profile_type:%s", t)
}

// profile specifies a profiles data (gzipped protobuf, json), and the types contained within it.
type profile struct {
	// name indicates profile type and format (e.g. cpu.pprof, metrics.json)
	name string
	pt   ProfileType
	data []byte
}

// batch is a collection of profiles of different types, collected at roughly the same time. It maps
// to what the Datadog UI calls a profile.
type batch struct {
	seq            uint64 // seq is the value of the profile_seq tag
	start, end     time.Time
	host           string
	profiles       []*profile
	endpointCounts map[string]uint64
}

func (b *batch) addProfile(p *profile) {
	b.profiles = append(b.profiles, p)
}

func (p *profiler) runProfile(pt ProfileType) ([]*profile, error) {
	start := now()
	t := pt.lookup()
	data, err := t.Collect(p)
	if err != nil {
		return nil, err
	}
	end := now()
	tags := append(p.cfg.tags.Slice(), pt.Tag())
	filename := t.Filename
	// TODO(fg): Consider making Collect() return the filename.
	if p.cfg.deltaProfiles && len(t.DeltaValues) > 0 {
		filename = "delta-" + filename
	}
	p.cfg.statsd.Timing("datadog.profiling.go.collect_time", end.Sub(start), tags, 1)
	return []*profile{{name: filename, pt: pt, data: data}}, nil
}

type deltaProfiler interface {
	Delta(curData []byte) ([]byte, error)
}

type pprofileDeltaProfiler struct {
	delta pprofutils.Delta
	prev  *pprofile.Profile
}

// newDeltaProfiler returns an initialized delta profiler based on cfg.deltaMethod
//
// - fastdelta: uses internal/fastdelta
// - comparing: executes both pprofile and fastdelta, comparing the two with statsd metrics
// - any other value: pprofile delta
//
// If value types are given (e.g. "alloc_space", "alloc_objects"),
// only those values will have deltas computed.
// Otherwise, deltas will be computed for every value.
func newDeltaProfiler(cfg *config, v ...pprofutils.ValueType) deltaProfiler {
	switch cfg.deltaMethod {
	// TODO: slowdelta and comparing implementation can be removed after 2023-04-11.
	case "slowdelta":
		return &pprofileDeltaProfiler{delta: pprofutils.Delta{SampleTypes: v}}
	case "comparing":
		return newComparingDeltaProfiler(
			cfg,
			&pprofileDeltaProfiler{delta: pprofutils.Delta{SampleTypes: v}},
			newFastDeltaProfiler(v...))
	default:
		return newFastDeltaProfiler(v...)
	}
}

// Delta derives the delta profile between curData and the profile passed to the
// previous call to Delta. The first call to Delta will return the profile
// unchanged.
func (d *pprofileDeltaProfiler) Delta(curData []byte) ([]byte, error) {
	curProf, err := pprofile.ParseData(curData)
	if err != nil {
		return nil, fmt.Errorf("delta prof parse: %v", err)
	}
	var deltaData []byte
	prevProf := d.prev
	if prevProf == nil {
		// First time deltaProfile gets called for a type, there is no prevProf. In
		// this case we emit the current profile as a delta profile.
		deltaData = curData
	} else {
		// Delta profiling is also implemented in the Go core, see commit below.
		// Unfortunately the core implementation isn't resuable via a API, so we do
		// our own delta calculation below.
		// https://github.com/golang/go/commit/2ff1e3ebf5de77325c0e96a6c2a229656fc7be50#diff-94594f8f13448da956b02997e50ca5a156b65085993e23bbfdda222da6508258R303-R304
		deltaProf, err := d.delta.Convert(prevProf, curProf)
		if err != nil {
			return nil, fmt.Errorf("delta prof merge: %v", err)
		}
		// TimeNanos is supposed to be the time the profile was collected, see
		// https://github.com/google/pprof/blob/master/proto/profile.proto.
		deltaProf.TimeNanos = curProf.TimeNanos
		// DurationNanos is the time period covered by the profile.
		deltaProf.DurationNanos = curProf.TimeNanos - prevProf.TimeNanos
		deltaBuf := &bytes.Buffer{}
		if err := deltaProf.Write(deltaBuf); err != nil {
			return nil, fmt.Errorf("delta prof write: %v", err)
		}
		deltaData = deltaBuf.Bytes()
	}
	// Keep the most recent profiles in memory for future diffing. This needs to
	// be taken into account when enforcing memory limits going forward.
	d.prev = curProf
	return deltaData, nil
}

type fastDeltaProfiler struct {
	dc  *fastdelta.DeltaComputer
	buf bytes.Buffer
	gzr gzip.Reader
	gzw *gzip.Writer
}

func newFastDeltaProfiler(v ...pprofutils.ValueType) deltaProfiler {
	fd := &fastDeltaProfiler{
		dc: fastdelta.NewDeltaComputer(v...),
	}
	fd.gzw = gzip.NewWriter(&fd.buf)
	return fd
}

func isGzipData(data []byte) bool {
	return bytes.HasPrefix(data, []byte{0x1f, 0x8b})
}

func (fdp *fastDeltaProfiler) Delta(data []byte) (b []byte, err error) {
	if isGzipData(data) {
		if err := fdp.gzr.Reset(bytes.NewReader(data)); err != nil {
			return nil, err
		}
		data, err = io.ReadAll(&fdp.gzr)
		if err != nil {
			return nil, fmt.Errorf("decompressing profile: %v", err)
		}
	}

	fdp.buf.Reset()
	fdp.gzw.Reset(&fdp.buf)

	if err = fdp.dc.Delta(data, fdp.gzw); err != nil {
		return nil, fmt.Errorf("error computing delta: %v", err)
	}
	if err = fdp.gzw.Close(); err != nil {
		return nil, fmt.Errorf("error flushing gzip writer: %v", err)
	}
	// The returned slice will be retained in case the profile upload fails,
	// so we need to return a copy of the buffer's bytes to avoid a data
	// race.
	b = make([]byte, len(fdp.buf.Bytes()))
	copy(b, fdp.buf.Bytes())
	return b, nil
}

type comparingDeltaProfiler struct {
	golden deltaProfiler
	sut    deltaProfiler
	tags   []string
	statsd StatsdClient
}

func newComparingDeltaProfiler(cfg *config, golden, systemUnderTest deltaProfiler) deltaProfiler {
	return &comparingDeltaProfiler{
		golden: golden,
		sut:    systemUnderTest,
		tags:   cfg.tags.Slice(),
		statsd: cfg.statsd,
	}
}

func (cdp *comparingDeltaProfiler) Delta(data []byte) (res []byte, err error) {
	sw := internal.NewStopwatch()
	res, err = cdp.golden.Delta(data)
	goldenDuration := sw.Tick()

	if err != nil {
		cdp.reportError(err.Error())
		return nil, err
	}

	resSut, err := cdp.sut.Delta(data)
	sutDuration := sw.Tick()

	cdp.reportTiming("golden", goldenDuration)
	cdp.reportTiming("sut", sutDuration)

	if err != nil {
		// sut errored, but return the golden result
		cdp.reportError(err.Error())
		return res, nil
	}

	pprofGolden, err := pprofile.ParseData(res)
	if err != nil {
		cdp.reportError(err.Error())
		return res, nil
	}

	pprofSut, err := pprofile.ParseData(resSut)
	if err != nil {
		cdp.reportError(err.Error())
		return res, nil
	}

	if pprofGolden.DurationNanos != pprofSut.DurationNanos {
		log.Error("profiles differ: golden_dur=%d sut_dur=%d", pprofGolden.DurationNanos, pprofSut.DurationNanos)
		cdp.reportError("compare_failed")
		return res, nil
	}

	pprofDiff, err := PprofDiff(pprofSut, pprofGolden)
	if err != nil {
		cdp.reportError(err.Error())
		return res, nil
	}

	cdp.reportTiming("overhead", sw.Tick())

	if len(pprofDiff.Sample) > 0 {
		var extraTags []string
		for _, vt := range pprofSut.SampleType {
			extraTags = append(extraTags, "sample_type:"+vt.Type)
		}
		pprofSut.Scale(-1) // so it prints correctly, PprofDiff mutated it
		log.Error("profiles differ: golden: %v\n\nsut: %v\n\ndiff:%v", pprofGolden, pprofSut, pprofDiff)
		cdp.reportError("compare_failed", extraTags...)
	}

	return res, nil
}

// interface hack to get around limitation of public profiler.StatsdClient
// The public interface only offers a Timing method _which_should_not_be_used_
// because histogram aggregation math is fundamentally broken.  It's unfortunate
// that the public interface exposed Timing instead of Distribution given
// Distribution was available at the time.
type statsdDistribution interface {
	// Distribution creates a distribution metric, prefer over Timing
	Distribution(event string, value float64, tags []string, rate float64) error
}

func (cdp *comparingDeltaProfiler) reportTiming(section string, dur time.Duration) {
	statsdClient, ok := cdp.statsd.(statsdDistribution)
	if !ok {
		return
	}
	_ = statsdClient.Distribution(
		"datadog.profiling.go.delta_compare.dist",
		float64(dur.Milliseconds()),
		append(cdp.tags, "section:"+section),
		1)
}

func (cdp *comparingDeltaProfiler) reportError(error string, extraTags ...string) {
	tags := append(cdp.tags, "msg:"+error)
	tags = append(tags, extraTags...)
	_ = cdp.statsd.Count("datadog.profiling.go.delta_compare.error", 1, tags, 1)
}

// PprofDiff computes the delta between all values b-a and returns them as a new
// profile. Samples that end up with a delta of 0 are dropped. WARNING: Profile
// a will be mutated by this function. You should pass a copy if that's
// undesirable.
//
// Deprecated: This function was introduced into our public API unintentionally.
// It will be removed in the next release. If you need this functionality,
// it can be implemented in two lines:
//
//	a.Scale(-1)
//	return pprofile.Merge([]*pprofile.Profile{a, b})
func PprofDiff(a, b *pprofile.Profile) (*pprofile.Profile, error) {
	a.Scale(-1)
	return pprofile.Merge([]*pprofile.Profile{a, b})
}

func goroutineDebug2ToPprof(r io.Reader, w io.Writer, t time.Time) (err error) {
	// gostackparse.Parse() has been extensively tested and should not crash
	// under any circumstances, but we really want to avoid crashing a customers
	// applications, so this code will recover from any unexpected panics and
	// return them as an error instead.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	goroutines, errs := gostackparse.Parse(r)

	functionID := uint64(1)
	locationID := uint64(1)

	p := &pprofile.Profile{
		TimeNanos: t.UnixNano(),
	}
	m := &pprofile.Mapping{ID: 1, HasFunctions: true}
	p.Mapping = []*pprofile.Mapping{m}
	p.SampleType = []*pprofile.ValueType{
		{
			Type: "waitduration",
			Unit: "nanoseconds",
		},
	}

	for _, g := range goroutines {
		sample := &pprofile.Sample{
			Value: []int64{g.Wait.Nanoseconds()},
			Label: map[string][]string{
				"state":   {g.State}, // TODO(fg) split into atomicstatus/waitreason?
				"lockedm": {fmt.Sprintf("%t", g.LockedToThread)},
			},
			NumUnit:  map[string][]string{"goid": {"id"}},
			NumLabel: map[string][]int64{"goid": {int64(g.ID)}},
		}

		// Treat the frame that created this goroutine as part of the stack so it
		// shows up in the stack trace / flame graph. Hopefully this will be more
		// useful than confusing for people.
		if g.CreatedBy != nil {
			// TODO(fg) should we modify the function name to include "created by"?
			g.Stack = append(g.Stack, g.CreatedBy)
		}

		// Based on internal discussion, the current strategy is to use virtual
		// frames to indicate truncated stacks, see [1] for how python/jd does it.
		// [1] https://github.com/DataDog/dd-trace-py/blob/e933d2485b9019a7afad7127f7c0eb541341cdb7/ddtrace/profiling/exporter/pprof.pyx#L117-L121
		if g.FramesElided {
			g.Stack = append(g.Stack, &gostackparse.Frame{
				Func: "...additional frames elided...",
			})
		}

		for _, call := range g.Stack {
			function := &pprofile.Function{
				ID:       functionID,
				Name:     call.Func,
				Filename: call.File,
			}
			p.Function = append(p.Function, function)
			functionID++

			location := &pprofile.Location{
				ID:      locationID,
				Mapping: m,
				Line: []pprofile.Line{{
					Function: function,
					Line:     int64(call.Line),
				}},
			}
			p.Location = append(p.Location, location)
			locationID++

			sample.Location = append(sample.Location, location)
		}

		p.Sample = append(p.Sample, sample)
	}

	// Put the error message in the pprof profiles as comments in case we need to
	// debug issues at some point.
	// TODO(fg) would be nice to also have a metric counter for this
	for _, err := range errs {
		p.Comments = append(p.Comments, "error: "+err.Error())
	}

	if err := p.CheckValid(); err != nil {
		return fmt.Errorf("marshalGoroutineDebug2Profile: %s", err)
	} else if err := p.Write(w); err != nil {
		return fmt.Errorf("marshalGoroutineDebug2Profile: %s", err)
	}
	return nil
}

// now returns current time in UTC.
func now() time.Time {
	return time.Now().UTC()
}
