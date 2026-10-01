//go:build fuzzexamplefixture

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fuzzexample

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/internal/mockci"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
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
	f.Fuzz(func(t *testing.T, seed int) {
		if seed < 32 {
			t.Parallel()
		}
		t.Cleanup(func() {
			if seed < 0 {
				t.Error("invalid seed")
			}
		})
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
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []string{"false", "true"} {
		t.Run(enabled, func(t *testing.T) {
			server := mockci.Start(net.SettingsResponseData{SubtestFeaturesEnabled: true}, nil, nil)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "-test.run=^FuzzCorpus$", "-test.timeout=25s")
			cmd.Env = append(os.Environ(), "DD_FUZZ_EXAMPLE_SCENARIO=corpus-child", "DD_CIVISIBILITY_ENABLED="+enabled)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("corpus failed: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), "CORPUS_MEMORY") {
				t.Fatalf("missing memory sample: %s", output)
			}
			t.Logf("enabled=%s %s", enabled, output)
			want := 0
			if enabled == "true" {
				want = corpusSize + 1
			}
			if got := server.EventTypeCount(constants.SpanTypeTest); got != want {
				t.Fatalf("native test count = %d, want %d", got, want)
			}
			for _, event := range server.Events() {
				if event.Type == constants.SpanTypeTest && event.Content.Meta[constants.TestStatus] != constants.TestStatusPass {
					t.Fatalf("unexpected corpus outcome: %+v", event)
				}
			}
		})
	}
}
