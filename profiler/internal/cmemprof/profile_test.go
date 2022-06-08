// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package cmemprof_test

import (
	"fmt"
	"regexp"
	"runtime"
	"sync"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/cmemprof"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/cmemprof/testallocator"
)

func TestCAllocationProfiler(t *testing.T) {
	var prof cmemprof.Profile
	prof.Start(1)

	testallocator.DoAllocC(32)
	testallocator.DoCalloc(32)
	testallocator.DoAllocC(32)
	testallocator.DoCalloc(32)

	pprof, err := prof.Stop()
	if err != nil {
		t.Fatalf("running profile: %s", err)
	}

	err = pprof.CheckValid()
	if err != nil {
		t.Fatalf("checking validity: %s", err)
	}
	original := pprof.Copy()
	found, _, _, _ := pprof.FilterSamplesByName(regexp.MustCompile("(DoAlloc|DoCalloc)"), nil, nil, nil)
	if !found {
		t.Logf("%s", original)
		t.Fatal("did not find any allocation samples")
	}
	if len(pprof.Sample) != 4 {
		t.Errorf("got %d samples, wanted 4", len(pprof.Sample))
	}
	for _, sample := range pprof.Sample {
		t.Log("--------")
		for _, loc := range sample.Location {
			t.Logf("%x %s", loc.Address, loc.Line[0].Function.Name)
		}
		if len(sample.Value) != 4 {
			t.Fatalf("sample should have 4 values")
		}
		count := sample.Value[0]
		size := sample.Value[1]
		t.Logf("count=%d, size=%d", count, size)
		if count != 1 {
			t.Errorf("got %d count, wanted 1", count)
		}
		if size != 32 {
			t.Errorf("got %d size, wanted 32", size)
		}
	}
}

// TestCgoMallocNoPanic checks that function which calls C.malloc will not cause
// the profiler to panic (by causing stack growth and invalidating the address
// where the result of C.malloc returns)
func TestCgoMallocNoPanic(t *testing.T) {
	var prof cmemprof.Profile
	prof.Start(1)

	testallocator.DoAllocGo(32)
	testallocator.DoAllocGo(32)
	testallocator.DoAllocGo(32)
	testallocator.DoAllocGo(32)

	_, err := prof.Stop()
	if err != nil {
		t.Fatalf("running profile: %s", err)
	}
}

// TestNewCgoThreadCrash checks that wrapping malloc does not cause creating a
// new Go runtime "m" (OS thread) to crash. For cgo programs, creating a new m
// calls malloc, and the malloc wrapper calls into Go code, which can't be done
// on a new m with no goroutine.
func TestNewCgoThreadCrash(t *testing.T) {
	var prof cmemprof.Profile
	prof.Start(1)

	var ready sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < runtime.GOMAXPROCS(0)*2; i++ {
		ready.Add(1)
		go func() {
			// By locking this OS thread we should force the Go
			// runtime to start making more threads
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			ready.Done()
			<-stop
		}()
	}
	ready.Wait()
	close(stop)

	_, err := prof.Stop()
	if err != nil {
		t.Fatalf("running profile: %s", err)
	}
}

func TestSampling(t *testing.T) {
	var prof cmemprof.Profile
	prof.Start(1024)
	for i := 0; i < 32; i++ {
		testallocator.DoAllocC(512)
	}
	pprof, err := prof.Stop()
	if err != nil {
		t.Fatalf("running profile: %s", err)
	}

	// The sampling rate is 1024 bytes, and the allocations are 512 bytes.
	// Each allocation is sampled with a probability of 1/2, so there is
	// probabilty 1 / (2 ** 32) (one in four billion) that there are no
	// samples. In other words, there should be at least on sample with very
	// high probability.

	original := pprof.Copy()
	t.Logf("%s", original)
	found, _, _, _ := pprof.FilterSamplesByName(regexp.MustCompile("AllocC"), nil, nil, nil)
	if !found {
		t.Fatal("did not find any allocation samples")
	}

	sample := pprof.Sample[0]
	count := sample.Value[0]
	size := sample.Value[1]

	// We sampled anywhere from 1 to 32 allocations, with an average of 16.
	// Each sample should count for 2 allocations (scaled by probability
	// 1/2) and count for 1024 bytes (allocation size 512 * 2). Thus the
	// count should be between 1 and 64, with an average value of 32, and
	// the size should be between 1024 and 1024*32, with an average of
	// 1024*16

	if (count < 1) || (count > 64) {
		t.Errorf("implausible count %d", count)
	}

	if (size < 1024) || (size > (1024 * 32)) {
		t.Errorf("implausible szie %d", size)
	}
}

func BenchmarkProfilerOverhead(b *testing.B) {
	baseline := func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			testallocator.DoAllocC(2048)
		}
	}
	b.Run("baseline", baseline)

	withProfiler := func(b *testing.B, rate int) {
		var prof cmemprof.Profile
		prof.Start(rate)
		baseline(b)
		_, err := prof.Stop()
		if err != nil {
			b.Fatal(err)
		}
	}

	for _, rate := range []int{512 * 1024, 128 * 1024, 32 * 1024, 1} {
		name := fmt.Sprintf("rate-%d", rate)
		b.Run(name, func(b *testing.B) {
			withProfiler(b, rate)
		})
	}
}
