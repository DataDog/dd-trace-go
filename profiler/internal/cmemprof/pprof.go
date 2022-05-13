// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package cmemprof

import (
	"os"
	"runtime"

	"github.com/google/pprof/profile"
)

func (c *Profile) build() *profile.Profile {
	// TODO: can we be sure that there won't be other allocation samples
	// ongoing that write to the sample map? Right now it's called with c.mu
	// held but it wouldn't be good if this (probably expensive) function
	// was holding the same lock that recordAllocationSample also wants to
	// hold
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
	// TODO: move this cache up into Profile?
	locations := make(map[uint64]*profile.Location)
	var funcid uint64
	for stack, event := range c.samples {
		psample := &profile.Sample{
			Value: []int64{int64(event.count), int64(event.bytes), 0, 0},
		}
		frames := runtime.CallersFrames(stack[:event.frames])
		for {
			frame, ok := frames.Next()
			if !ok {
				break
			}
			addr := uint64(frame.PC)
			loc, ok := locations[addr]
			if !ok {
				loc = &profile.Location{
					ID:      uint64(len(locations)) + 1,
					Mapping: m,
					Address: uint64(frame.PC),
				}
				funcid++
				function := &profile.Function{
					ID:       funcid,
					Filename: frame.File,
					Name:     frame.Function,
				}
				p.Function = append(p.Function, function)
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
