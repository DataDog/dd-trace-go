// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"bytes"
	"errors"
	"io"
	"runtime/pprof"
	"time"
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
	// BlockProfile shows where goroutines block waiting on synchronization primitives
	// (including timer channels). Block profile is not enabled by default.
	BlockProfile
	// MutexProfile reports the lock contentions. When you think your CPU is not fully utilized due
	// to a mutex contention, use this profile. Mutex profile is not enabled by default.
	MutexProfile
	// GoroutineProfile reports stack traces of all current goroutines
	GoroutineProfile
)

func (t ProfileType) String() string {
	switch t {
	case HeapProfile:
		return "heap"
	case CPUProfile:
		return "cpu"
	case MutexProfile:
		return "mutex"
	case BlockProfile:
		return "block"
	case GoroutineProfile:
		return "goroutine"
	default:
		return "unknown"
	}
}

// profile specifies a pprof's data (gzipped protobuf), and the types contained
// within it.
type profile struct {
	types []string
	data  []byte
}

// batch is a collection of profiles of different types, collected at roughly the same time. It maps
// to what the Datadog UI calls a profile.
type batch struct {
	start, end time.Time
	host       string
	profiles   []*profile
}

func (b *batch) addProfile(p *profile) {
	b.profiles = append(b.profiles, p)
}

func (p *profiler) runProfile(t ProfileType) (*profile, error) {
	switch t {
	case HeapProfile:
		return heapProfile(p.cfg)
	case CPUProfile:
		return cpuProfile(p.cfg)
	case MutexProfile:
		return mutexProfile(p.cfg)
	case BlockProfile:
		return blockProfile(p.cfg)
	case GoroutineProfile:
		return goroutineProfile(p.cfg)
	default:
		return nil, errors.New("profile type not implemented")
	}
}

// writeHeapProfile writes the heap profile; replaced in tests
var writeHeapProfile = pprof.WriteHeapProfile

func heapProfile(cfg *config) (*profile, error) {
	var buf bytes.Buffer
	start := now()
	if err := writeHeapProfile(&buf); err != nil {
		return nil, err
	}
	end := now()
	tags := append(cfg.tags, "profile_type:heap")
	cfg.statsd.Timing("datadog.profiler.go.collect_time", end.Sub(start), tags, 1)
	return &profile{
		types: []string{"alloc_objects", "alloc_space", "inuse_objects", "inuse_space"},
		data:  buf.Bytes(),
	}, nil
}

var (
	// startCPUProfile starts the CPU profile; replaced in tests
	startCPUProfile = pprof.StartCPUProfile
	// stopCPUProfile stops the CPU profile; replaced in tests
	stopCPUProfile = pprof.StopCPUProfile
)

func cpuProfile(cfg *config) (*profile, error) {
	var buf bytes.Buffer
	start := now()
	if err := startCPUProfile(&buf); err != nil {
		return nil, err
	}
	time.Sleep(cfg.cpuDuration)
	stopCPUProfile()
	end := now()
	tags := append(cfg.tags, "profile_type:cpu")
	cfg.statsd.Timing("datadog.profiler.go.collect_time", end.Sub(start), tags, 1)
	return &profile{
		types: []string{"samples", "cpu"},
		data:  buf.Bytes(),
	}, nil
}

// lookpupProfile looks up the profile with the given name and writes it to w. It returns
// any errors encountered in the process. It is replaced in tests.
var lookupProfile = func(name string, w io.Writer) error {
	prof := pprof.Lookup(name)
	if prof == nil {
		return errors.New("profile not found")
	}
	return prof.WriteTo(w, 0)
}

func blockProfile(cfg *config) (*profile, error) {
	var buf bytes.Buffer
	start := now()
	if err := lookupProfile("block", &buf); err != nil {
		return nil, err
	}
	end := now()
	tags := append(cfg.tags, "profile_type:block")
	cfg.statsd.Timing("datadog.profiler.go.collect_time", end.Sub(start), tags, 1)
	return &profile{
		types: []string{"delay"},
		data:  buf.Bytes(),
	}, nil
}

func mutexProfile(cfg *config) (*profile, error) {
	var buf bytes.Buffer
	start := now()
	if err := lookupProfile("mutex", &buf); err != nil {
		return nil, err
	}
	end := now()
	tags := append(cfg.tags, "profile_type:mutex")
	cfg.statsd.Timing("datadog.profiler.go.collect_time", end.Sub(start), tags, 1)
	return &profile{
		types: []string{"contentions"},
		data:  buf.Bytes(),
	}, nil
}

func goroutineProfile(cfg *config) (*profile, error) {
	var buf bytes.Buffer
	start := now()
	if err := lookupProfile("goroutine", &buf); err != nil {
		return nil, err
	}
	end := now()
	tags := append(cfg.tags, "profile_type:goroutine")
	cfg.statsd.Timing("datadog.profiler.go.collect_time", end.Sub(start), tags, 1)
	return &profile{
		types: []string{"goroutines"},
		data:  buf.Bytes(),
	}, nil
}

// now returns current time in UTC.
func now() time.Time {
	return time.Now().UTC()
}
