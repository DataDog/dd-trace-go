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
			// Limits nest, and a descendant may declare more than an ancestor allows. The
			// lower ancestor is the ceiling that actually binds.
			name: "a-lower-ancestor-limit-beats-the-process-own",
			files: map[string]string{
				fixtureSelfCgrp: "0::/kubepods/pod123/container456\n",
				"sys/fs/cgroup/kubepods/pod123/container456/memory.max": "4294967296",
				"sys/fs/cgroup/kubepods/pod123/memory.max":              "536870912",
			},
			wantLimit: 536870912,
			wantOK:    true,
		},
		{
			// The same ancestry the other way round: the leaf is the lowest, so it wins.
			name: "a-lower-process-own-limit-beats-the-ancestor",
			files: map[string]string{
				fixtureSelfCgrp: "0::/kubepods/pod123/container456\n",
				"sys/fs/cgroup/kubepods/pod123/container456/memory.max": "536870912",
				"sys/fs/cgroup/kubepods/pod123/memory.max":              "4294967296",
			},
			wantLimit: 536870912,
			wantOK:    true,
		},
		{
			// A level that sets no limit does not constrain, so the walk has to keep going
			// rather than treat it as the answer.
			name: "an-unlimited-level-does-not-mask-a-limited-ancestor",
			files: map[string]string{
				fixtureSelfCgrp: "0::/kubepods/pod123/container456\n",
				"sys/fs/cgroup/kubepods/pod123/container456/memory.max": "max\n",
				"sys/fs/cgroup/kubepods/memory.max":                     "268435456",
			},
			wantLimit: 268435456,
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

// A Go memory limit and a cgroup limit can both be present and need not agree. Each
// subtest is not parallel: the Go memory limit is process-wide.
func TestMemoryBudget(t *testing.T) {
	// math.MaxInt64 is what the runtime reports for an unset GOMEMLIMIT, so it stands in
	// for "no Go limit" here. It is not a budget, and must not be read as one: 0.5% of it
	// would pin every ring to the cap.
	const noGoLimit = int64(math.MaxInt64)

	tests := []struct {
		name       string
		goLimit    int64
		cgroup     string
		wantBudget int64
		wantFound  bool
	}{
		{
			name:      "neither-limit-is-present",
			goLimit:   noGoLimit,
			wantFound: false,
		},
		{
			name:       "only-a-go-limit",
			goLimit:    2 << 30,
			wantBudget: 2 << 30,
			wantFound:  true,
		},
		{
			name:       "only-a-cgroup-limit",
			goLimit:    noGoLimit,
			cgroup:     "536870912",
			wantBudget: 536870912,
			wantFound:  true,
		},
		{
			// The deliberate case: GOMEMLIMIT set below the container limit to leave room
			// for non-Go memory.
			name:       "a-go-limit-below-the-cgroup-limit-wins",
			goLimit:    1 << 30,
			cgroup:     "2147483648",
			wantBudget: 1 << 30,
			wantFound:  true,
		},
		{
			// The accidental case: one value baked into an image or chart and reused across
			// differently sized deployments. A Go memory limit is a soft GC target and does
			// not lift the container's hard ceiling, so the cgroup has to win.
			name:       "a-go-limit-above-the-cgroup-limit-does-not-lift-it",
			goLimit:    8 << 30,
			cgroup:     "536870912",
			wantBudget: 536870912,
			wantFound:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.cgroup != "" {
				path := filepath.Join(root, "sys/fs/cgroup", cgroupV2MemoryLimit)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, []byte(tt.cgroup), 0o644))
			}

			// A negative argument reads the limit without setting it.
			previous := debug.SetMemoryLimit(-1)
			t.Cleanup(func() { debug.SetMemoryLimit(previous) })
			debug.SetMemoryLimit(tt.goLimit)

			budget, found := memoryBudget(root)

			assert.Equal(t, tt.wantFound, found)
			assert.Equal(t, tt.wantBudget, budget)
		})
	}
}

// Both ways a higher limit can shadow the one that actually binds, stated as the ring
// size each produced before it was fixed. A 512 MiB container is below the floor, so in
// both cases the correct answer is the floor rather than a ring sized from the higher
// number — ~30 MB (5.9% of that container) and 21.5 MB respectively.
func TestRingSlotsHonorsTheLowestEffectiveLimit(t *testing.T) {
	tests := []struct {
		name    string
		goLimit int64
		files   map[string]string
	}{
		{
			// A Go memory limit is a soft GC target and does not lift the container's.
			name:    "a-go-limit-above-the-container-limit",
			goLimit: 8 << 30,
			files:   map[string]string{"sys/fs/cgroup/memory.max": "536870912"},
		},
		{
			// Limits nest, so a 4 GiB leaf under a 512 MiB ancestor is still capped at
			// 512 MiB. This gave a 71,582-slot ring.
			name:    "a-leaf-cgroup-limit-above-its-ancestor",
			goLimit: math.MaxInt64,
			files: map[string]string{
				"proc/self/cgroup": "0::/kubepods/pod123/container456\n",
				"sys/fs/cgroup/kubepods/pod123/container456/memory.max": "4294967296",
				"sys/fs/cgroup/kubepods/pod123/memory.max":              "536870912",
			},
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

			previous := debug.SetMemoryLimit(-1)
			t.Cleanup(func() { debug.SetMemoryLimit(previous) })
			debug.SetMemoryLimit(tt.goLimit)

			assert.Equal(t, minRingSlots, ringSlots(memoryBudget(root)))
		})
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
