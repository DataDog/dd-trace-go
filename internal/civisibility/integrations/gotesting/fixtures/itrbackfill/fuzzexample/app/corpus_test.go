// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import (
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/internal/fuzzfixture"
)

const corpusSize = 10000

var corpusHeapBefore, corpusHeapRetained uint64

func FuzzCorpus(f *testing.F) {
	for seed := range corpusSize {
		f.Add(seed)
	}
	runtime.GC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	corpusHeapBefore = mem.HeapAlloc
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, seed int) {
		if seed < 32 {
			t.Parallel()
		}
		t.Cleanup(func() {
			if seed < 0 {
				t.Error("invalid seed")
			}
		})
		if os.Getenv("DD_FUZZ_CORPUS_SKIP") == "true" {
			t.Skip("corpus skip sentinel")
		}
	})
	runtime.GC()
	runtime.ReadMemStats(&mem)
	corpusHeapRetained = mem.HeapAlloc
}

func reportCorpusMemory() {
	runtime.GC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	fmt.Printf("CORPUS_MEMORY seeds=%d retained_delta=%d after_run_delta=%d\n", corpusSize, int64(corpusHeapRetained)-int64(corpusHeapBefore), int64(mem.HeapAlloc)-int64(corpusHeapBefore))
}

func TestFuzzCorpusLifecycle(t *testing.T) {
	if os.Getenv("DD_FUZZ_EXAMPLE_MODE") == "" {
		t.Skip("fixture requires the fuzz/example harness")
	}
	for _, enabled := range []string{"false", "true", "skip"} {
		t.Run(enabled, func(t *testing.T) {
			fuzzfixture.CheckCorpusLifecycle(t, enabled, corpusSize)
		})
	}
}
