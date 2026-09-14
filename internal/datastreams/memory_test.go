// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRingSlots(t *testing.T) {
	tests := []struct {
		name   string
		budget int64
		found  bool
		want   int
	}{
		{name: "no-budget-found-keeps-the-historical-size", budget: 0, found: false, want: minRingSlots},
		// A budget that was found but is nonsensical must not produce a zero-length ring:
		// push would panic on a division by zero.
		{name: "zero-budget-keeps-the-historical-size", budget: 0, found: true, want: minRingSlots},
		{name: "negative-budget-keeps-the-historical-size", budget: -1, found: true, want: minRingSlots},
		{name: "a-budget-found-but-unusably-small", budget: 1, found: true, want: minRingSlots},
		{name: "128MiB-container-is-below-the-floor", budget: 128 << 20, found: true, want: minRingSlots},
		{name: "512MiB-container-is-still-below-the-floor", budget: 512 << 20, found: true, want: minRingSlots},
		// The exact point at which the derived size meets the floor, so growth starts just
		// above it rather than jumping.
		{name: "600MB-meets-the-floor-exactly", budget: 600_000_000, found: true, want: minRingSlots},
		{name: "1GiB-grows", budget: 1 << 30, found: true, want: 17895},
		{name: "2GiB-grows", budget: 2 << 30, found: true, want: 35791},
		{name: "4GiB-grows", budget: 4 << 30, found: true, want: 71582},
		{name: "6GB-meets-the-cap-exactly", budget: 6_000_000_000, found: true, want: maxRingSlots},
		{name: "8GiB-is-capped", budget: 8 << 30, found: true, want: maxRingSlots},
		{name: "64GiB-is-capped", budget: 64 << 30, found: true, want: maxRingSlots},
		// int is 32 bits on the 32-bit targets scripts/cross_build.sh covers, so the clamp
		// has to happen in int64 before the conversion.
		{name: "an-absurd-budget-does-not-overflow-int", budget: math.MaxInt64, found: true, want: maxRingSlots},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ringSlots(tt.budget, tt.found))
		})
	}
}

func TestCgroupMemoryLimit(t *testing.T) {
	const fixtureSelfCgrp = "proc/self/cgroup"

	tests := []struct {
		name      string
		files     map[string]string
		wantLimit int64
		wantOK    bool
	}{
		{
			// The macOS and Windows case: nothing to read, and the caller falls back to the
			// floor rather than treating it as a failure.
			name:      "no-cgroup-filesystem-at-all",
			files:     map[string]string{},
			wantLimit: 0,
			wantOK:    false,
		},
		{
			name:      "cgroup-v2-at-the-mount-root",
			files:     map[string]string{"sys/fs/cgroup/memory.max": "536870912"},
			wantLimit: 536870912,
			wantOK:    true,
		},
		{
			name:      "cgroup-v2-unlimited-reads-as-absent",
			files:     map[string]string{"sys/fs/cgroup/memory.max": "max\n"},
			wantLimit: 0,
			wantOK:    false,
		},
		{
			name:      "cgroup-v1-at-the-mount-root",
			files:     map[string]string{"sys/fs/cgroup/memory/memory.limit_in_bytes": "268435456"},
			wantLimit: 268435456,
			wantOK:    true,
		},
		{
			// cgroup v1 spells "unlimited" as a huge sentinel rather than a keyword.
			name:      "cgroup-v1-sentinel-reads-as-absent",
			files:     map[string]string{"sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712"},
			wantLimit: 0,
			wantOK:    false,
		},
		{
			name:      "unparseable-limit-reads-as-absent",
			files:     map[string]string{"sys/fs/cgroup/memory.max": "not-a-number"},
			wantLimit: 0,
			wantOK:    false,
		},
		{
			// A hybrid hierarchy exposes both; v2 is the authoritative one.
			name: "cgroup-v2-is-preferred-over-v1",
			files: map[string]string{
				"sys/fs/cgroup/memory.max":                   "111111",
				"sys/fs/cgroup/memory/memory.limit_in_bytes": "222222",
			},
			wantLimit: 111111,
			wantOK:    true,
		},
		{
			// The host cgroup namespace case: the mount root carries no limit, and
			// /proc/self/cgroup names where the process actually lives.
			name: "cgroup-v2-in-the-process-own-cgroup",
			files: map[string]string{
				fixtureSelfCgrp: "0::/kubepods/pod123/container456\n",
				"sys/fs/cgroup/kubepods/pod123/container456/memory.max": "1073741824",
			},
			wantLimit: 1073741824,
			wantOK:    true,
		},
		{
			// The limit is often set on the pod rather than the container, so the walk up
			// towards the root has to keep looking.
			name: "cgroup-v2-limit-on-an-ancestor",
			files: map[string]string{
				fixtureSelfCgrp: "0::/kubepods/pod123/container456\n",
				"sys/fs/cgroup/kubepods/pod123/memory.max": "2147483648",
			},
			wantLimit: 2147483648,
			wantOK:    true,
		},
		{
			// cgroup v1 lists one hierarchy per line; only the memory controller matters.
			name: "cgroup-v1-picks-the-memory-controller-line",
			files: map[string]string{
				fixtureSelfCgrp: "5:cpu,cpuacct:/some/cpu/path\n4:memory:/mem/path\n",
				"sys/fs/cgroup/memory/mem/path/memory.limit_in_bytes": "99999",
			},
			wantLimit: 99999,
			wantOK:    true,
		},
		{
			// An unreadable /proc/self/cgroup must degrade to reading the mount root, not
			// to reading nothing.
			name:      "a-missing-proc-self-cgroup-still-reads-the-mount-root",
			files:     map[string]string{"sys/fs/cgroup/memory.max": "424242"},
			wantLimit: 424242,
			wantOK:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for path, content := range tt.files {
				full := filepath.Join(root, path)
				require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
				require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
			}

			limit, ok := cgroupMemoryLimit(root)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantLimit, limit)
		})
	}
}

// The Go runtime reads no cgroup limit itself, so a declared GOMEMLIMIT is the more
// specific signal and has to win over the container's.
//
// Not parallel: the memory limit is process-wide.
func TestMemoryBudgetPrefersTheGoMemoryLimit(t *testing.T) {
	const limit = int64(2) << 30

	// A negative argument reads the limit without setting it.
	previous := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(previous) })

	debug.SetMemoryLimit(limit)

	budget, found := memoryBudget()

	assert.True(t, found)
	assert.Equal(t, limit, budget)
}

// An unset GOMEMLIMIT reports math.MaxInt64 rather than an error. That is not a budget
// and must not be read as one: 0.5% of math.MaxInt64 would pin every ring to the cap.
func TestMemoryBudgetTreatsTheRuntimeDefaultAsUnset(t *testing.T) {
	previous := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(previous) })

	debug.SetMemoryLimit(math.MaxInt64)

	// Whether a budget is found now depends on the host's cgroups, which the test cannot
	// control. Either way it must not be the runtime sentinel.
	budget, found := memoryBudget()

	assert.NotEqual(t, int64(math.MaxInt64), budget)
	if !found {
		assert.Zero(t, budget)
	}
}

// ringBytesPerSlot estimates the struct plus what it points at, so it has to stay above
// the struct alone. Adding a field to processorInput should force the estimate to be
// revisited rather than silently invalidate it.
func TestRingBytesPerSlotCoversProcessorInput(t *testing.T) {
	size := reflect.TypeFor[processorInput]().Size()

	assert.LessOrEqual(t, size, uintptr(ringBytesPerSlot),
		"processorInput grew past the per-slot estimate; re-measure ringBytesPerSlot")
}
