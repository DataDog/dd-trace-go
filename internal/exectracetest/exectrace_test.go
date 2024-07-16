// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// exectracetest tests execution tracer-related functionality.
// The official execution trace parser lives in golang.org/x/exp,
// which we generally should avoid upgrading as it is prone
// to breaking changes which could affect our customers.
// So, this package lives in a separate module in order to
// freely upgrade golang.org/x/exp/trace as the trace format changes.
package exectracetest

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"reflect"
	"regexp"
	"runtime/pprof"
	"runtime/trace"
	"sort"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	exptrace "golang.org/x/exp/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type discardLogger struct{}

func (discardLogger) Log(msg string) {}

// collectTestData runs f under the CPU profiler and execution tracer
func collectTestData(t *testing.T, f func()) (*profile.Profile, []exptrace.Event) {
	cpuProf := new(bytes.Buffer)
	execTrace := new(bytes.Buffer)
	if pprof.StartCPUProfile(cpuProf) != nil {
		t.Skip("CPU profiler already running")
	}
	defer pprof.StopCPUProfile() // okay to double-stop
	if trace.Start(execTrace) != nil {
		t.Skip("execution tracer already running")
	}
	defer trace.Stop() // okay to double-stop

	f()

	trace.Stop()
	pprof.StopCPUProfile()

	pprof, err := profile.Parse(cpuProf)
	if err != nil {
		t.Fatalf("parsing profile: %s", err)
	}
	reader, err := exptrace.NewReader(execTrace)
	if err != nil {
		t.Fatalf("reading execution trace: %s", err)
	}
	var traceEvents []exptrace.Event
	for {
		ev, err := reader.ReadEvent()
		if err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("reading event: %s", err)
		}
		traceEvents = append(traceEvents, ev)
	}
	return pprof, traceEvents
}

func waste(d time.Duration) {
	now := time.Now()
	for time.Since(now) < d {
	}
}

func TestSpanDoubleFinish(t *testing.T) {
	generate := func(d time.Duration) {
		tracer.Start(tracer.WithLogger(discardLogger{}))
		defer tracer.Stop()
		foo, ctx := tracer.StartSpanFromContext(context.Background(), "foo")
		bar, _ := tracer.StartSpanFromContext(ctx, "bar")
		bar.Finish()
		foo.Finish()
		bar.Finish()
		// If we don't handle double finish right, we will see CPU profile samples
		// for waste tagged with the trace/span IDs from foo
		waste(d)
	}

	var (
		pprof     *profile.Profile
		execTrace []exptrace.Event
		retries   int
	)
	const maxRetries = 5
	for duration := 30 * time.Millisecond; retries < maxRetries; retries++ {
		pprof, execTrace = collectTestData(t, func() {
			generate(duration)
		})
		focus, _, _, _ := pprof.FilterSamplesByName(regexp.MustCompile("waste"), nil, nil, nil)
		if !focus || len(execTrace) == 0 {
			// Retry with longer run to reduce flake likelihood
			duration *= 2
			continue
		}
		break
	}
	if retries == maxRetries {
		t.Fatalf("could not collect sufficient data after %d retries", maxRetries)
	}

	// Check CPU profile: we should have un-set the labels
	// for the goroutine by the time waste5 runs
	for _, sample := range pprof.Sample {
		if labels := sample.Label; len(labels) > 0 {
			t.Errorf("unexpected labels for sample: %+v", labels)
		}
	}

	// Check execution trace: we should not emit a second task end event
	// even though we finish the "bar" span twice
	taskEvents := make(map[exptrace.TaskID][]exptrace.Event)
	for _, ev := range execTrace {
		switch ev.Kind() {
		case exptrace.EventTaskBegin, exptrace.EventTaskEnd:
			id := ev.Task().ID
			taskEvents[id] = append(taskEvents[id], ev)
		}
	}
	for id, events := range taskEvents {
		if len(events) > 2 {
			t.Errorf("extraneous events for task %d: %v", id, events)
		}
	}
}

// TODO: move database/sql tests here? likely requires copying over contrib/sql/internal.MockDriver

func TestExecutionTraceSpans(t *testing.T) {
	var root, child tracer.Span
	_, execTrace := collectTestData(t, func() {
		tracer.Start(tracer.WithLogger(discardLogger{}))
		defer tracer.Stop()
		var ctx context.Context
		root, ctx = tracer.StartSpanFromContext(context.Background(), "root")
		child, _ = tracer.StartSpanFromContext(ctx, "child")
		root.Finish()
		child.Finish()
	})

	type traceSpan struct {
		name   string
		parent string
		spanID uint64
	}

	spans := make(map[exptrace.TaskID]*traceSpan)
	for _, ev := range execTrace {
		switch ev.Kind() {
		case exptrace.EventTaskBegin:
			task := ev.Task()
			var parent string
			if p, ok := spans[task.Parent]; ok {
				parent = p.name
			}
			spans[task.ID] = &traceSpan{
				name:   task.Type,
				parent: parent,
			}
		case exptrace.EventLog:
			span, ok := spans[ev.Log().Task]
			if !ok {
				continue
			}
			if key := ev.Log().Category; key == "datadog.uint64_span_id" {
				span.spanID = binary.LittleEndian.Uint64([]byte(ev.Log().Message))
			}
		}
	}

	want := []traceSpan{
		{name: "child", parent: "root", spanID: child.Context().SpanID()},
		{name: "root", spanID: root.Context().SpanID()},
	}
	var got []traceSpan
	for _, v := range spans {
		got = append(got, *v)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].name < got[j].name })

	if !reflect.DeepEqual(want, got) {
		t.Errorf("wanted spans %+v, got %+v", want, got)
	}
}
