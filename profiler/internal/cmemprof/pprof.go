// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package cmemprof

import (
	"os"
	"runtime"
	"strings"

	"github.com/google/pprof/profile"
)

func (c *Profile) build() *profile.Profile {
	// TODO: can we be sure that there won't be other allocation samples
	// ongoing that write to the sample map? Right now it's called with c.mu
	// held but it wouldn't be good if this (probably expensive) function
	// was holding the same lock that recordAllocationSample also wants to
	// hold

	// This profile is intended to be merged into the Go runtime allocation
	// profile.  The profile.Merge function requires that several fields in
	// the merged profile.Profile objects match in order for the merge to be
	// successful. Refer to
	//
	// https://pkg.go.dev/github.com/google/pprof/profile#Merge
	//
	// In particular, the PeriodType and SampleType fields must match, and
	// every sample must have the correct number of values. The TimeNanos
	// field can be left 0, and the TimeNanos field of the Go allocation
	// profile will be used.
	p := &profile.Profile{}
	m := &profile.Mapping{
		ID:   1,
		File: os.Args[0], // XXX: Is there a better way to get the executable?
	}
	p.PeriodType = &profile.ValueType{Type: "space", Unit: "bytes"}
	p.Period = 1
	p.Mapping = []*profile.Mapping{m}
	p.SampleType = []*profile.ValueType{
		{
			Type: "alloc_objects",
			Unit: "count",
		},
		{
			Type: "alloc_space",
			Unit: "bytes",
		},
		// This profiler doesn't actually do heap profiling yet, but in
		// order to view Go allocation profiles and C allocation
		// profiles at the same time, the sample types need to be the
		// same
		{
			Type: "inuse_objects",
			Unit: "count",
		},
		{
			Type: "inuse_space",
			Unit: "bytes",
		},
	}
	// TODO: move these cache up into Profile?
	functions := make(map[string]*profile.Function)
	locations := make(map[uint64]*profile.Location)
	for stack, event := range c.samples {
		psample := &profile.Sample{
			Value: []int64{int64(event.count), int64(event.bytes), 0, 0},
		}
		var length int
		for _, pc := range stack {
			if pc == 0 {
				break
			}
			length++
		}
		frames := runtime.CallersFrames(stack[:length])
		for {
			frame, ok := frames.Next()
			if !ok {
				break
			}
			// runtime.Callers has a skip argument but we can't skip
			// the exported allocation sample function with it, so
			// we manually prune it here.
			if frame.Function == "recordAllocationSample" {
				continue
			}
			if strings.HasPrefix(frame.Function, "profile_allocation") {
				continue
			}
			addr := uint64(frame.PC)
			loc, ok := locations[addr]
			if !ok {
				loc = &profile.Location{
					ID:      uint64(len(locations)) + 1,
					Mapping: m,
					Address: uint64(frame.PC),
				}
				function, ok := functions[frame.Function]
				if !ok {
					function = &profile.Function{
						ID:       uint64(len(p.Function)) + 1,
						Filename: frame.File,
						Name:     frame.Function,
					}
					// On Linux, allocation functions end up
					// with a "__wrap_" prefix, which we
					// remove to avoid confusion ("where did
					// __wrap_malloc come from?")
					function.Name = strings.TrimPrefix(function.Name, "__wrap_")
					p.Function = append(p.Function, function)
				}
				loc.Line = append(loc.Line, profile.Line{
					Function: function,
					Line:     int64(frame.Line),
				})
				locations[addr] = loc
				p.Location = append(p.Location, loc)
			}
			psample.Location = append(psample.Location, loc)
		}
		p.Sample = append(p.Sample, psample)
	}
	return p
}
