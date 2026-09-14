// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

const (
	// ringBytesPerSlot estimates the steady-state cost of one ring slot: the pointer
	// cell plus the processorInput it retains, which is 248 bytes of struct on 64-bit
	// plus the edgeTags slice and transaction ID backing allocated per call. It is a
	// steady-state cost rather than a transient one because pop does not clear the slot
	// it reads: a slot holds its last value until a later push laps around and
	// overwrites it. An estimate by nature, kept as a single named constant so it can be
	// re-measured without touching the sizing logic below.
	ringBytesPerSlot = 300

	// ringBudgetShare is the fraction of the process memory budget the ring may occupy.
	ringBudgetShare = 0.005

	// minRingSlots is the size the ring has always had, and the size it keeps whenever
	// the budget cannot be read or is too small to justify more. The heuristic only ever
	// grows the ring past this, so those deployments behave exactly as they did before.
	// At ringBudgetShare it corresponds to a 600 MB budget, which leaves the heuristic
	// inert below that.
	minRingSlots = 10_000

	// maxRingSlots caps the ring at ~30 MB at ringBytesPerSlot, reached at a 6 GB budget.
	maxRingSlots = 100_000
)

const (
	// The cgroup filesystem, and the memory limit file within a v2 and a v1 hierarchy.
	// Paths are relative so tests can resolve them against a fixture tree.
	cgroupMountPath     = "sys/fs/cgroup"
	cgroupV2MemoryLimit = "memory.max"
	cgroupV1MemoryLimit = "memory/memory.limit_in_bytes"
	procSelfCgroupPath  = "proc/self/cgroup"

	// cgroup v1 reports "unlimited" as a huge sentinel rather than a keyword, so treat
	// implausibly large values as absent.
	cgroupMemoryUnlimited = int64(1) << 62
)

// ringSlots converts a process memory budget into a number of ring slots. found reports
// whether a budget was established at all, as distinct from a zero one.
func ringSlots(budget int64, found bool) int {
	if !found || budget <= 0 {
		return minRingSlots
	}
	slots := int64(float64(budget) * ringBudgetShare / ringBytesPerSlot)
	// Clamp before narrowing: int is 32 bits on the linux/386, linux/arm and windows/386
	// targets scripts/cross_build.sh covers, and an unclamped slots can exceed it.
	return int(min(max(slots, minRingSlots), maxRingSlots))
}

// memoryBudget reports how many bytes this process may use, and whether any limit was
// found at all.
func memoryBudget() (int64, bool) {
	// A negative argument reads the limit without setting it. Unset, the runtime reports
	// math.MaxInt64 rather than an error, which is not a budget. Reading the runtime
	// rather than GOMEMLIMIT also picks up an application that called SetMemoryLimit
	// itself, and avoids a bare os.Getenv, which this repository does not permit.
	if limit := debug.SetMemoryLimit(-1); limit > 0 && limit < math.MaxInt64 {
		return limit, true
	}
	return cgroupMemoryLimit("/")
}

// cgroupMemoryLimit reports the container memory limit in bytes, preferring cgroup v2
// over v1. root is the filesystem root to resolve the cgroup paths against.
//
// This is the only signal that a ceiling exists for a container that has not set
// GOMEMLIMIT, which is the common case: the Go runtime reads no cgroup memory limit in
// any version up to and including 1.26, so the limit it reports is math.MaxInt64.
//
// The limit is not necessarily at the root of the cgroup mount. With a private cgroup
// namespace, the common container case, the process sees its own cgroup as "/" and the
// mount root is correct. Running in the host cgroup namespace, as some Kubernetes
// runtimes still do, the mount root is the host's cgroup and reading it would report no
// limit at all or the whole machine's. /proc/self/cgroup names the path this process
// actually lives under, so try that first and walk up towards the root, since the limit
// may be set on an ancestor such as the pod rather than the container.
//
// No build tags and no GOOS branch: on platforms without cgroups these paths simply do
// not exist, every read fails, and the caller falls back to minRingSlots. The cost is a
// handful of failed opens once per process.
func cgroupMemoryLimit(root string) (int64, bool) {
	for _, limitFile := range []string{cgroupV2MemoryLimit, cgroupV1MemoryLimit} {
		mount := filepath.Join(root, cgroupMountPath)
		// The v1 memory controller is mounted in its own subdirectory, which the relative
		// cgroup path is expressed underneath.
		base := filepath.Join(mount, filepath.Dir(limitFile))
		name := filepath.Base(limitFile)

		for _, relative := range cgroupSelfPaths(root) {
			if limit, ok := readCgroupMemoryLimit(filepath.Join(base, relative, name)); ok {
				return limit, true
			}
		}
	}

	return 0, false
}

// cgroupSelfPaths returns the cgroup paths to try, from the process's own cgroup up to
// the mount root. The root is always included so a missing or unreadable
// /proc/self/cgroup degrades to reading the mount root rather than to nothing.
func cgroupSelfPaths(root string) []string {
	content, err := os.ReadFile(filepath.Join(root, procSelfCgroupPath))
	if err != nil {
		return []string{"/"}
	}

	// Lines are "hierarchy:controllers:path"; cgroup v2 uses the single "0::<path>".
	var self string
	for line := range strings.SplitSeq(string(content), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(fields) != 3 || fields[2] == "" {
			continue
		}
		// Prefer the v2 unified entry or the v1 memory controller; anything else describes
		// a hierarchy that does not carry the memory limit.
		if fields[0] == "0" || strings.Contains(fields[1], "memory") {
			self = fields[2]
			break
		}
	}

	paths := []string{}
	for current := self; current != "" && current != "/" && current != "."; current = filepath.Dir(current) {
		paths = append(paths, current)
	}

	return append(paths, "/")
}

// readCgroupMemoryLimit reads a single cgroup memory limit file, reporting false when
// the file is absent, unparseable, or denotes no limit.
func readCgroupMemoryLimit(path string) (int64, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}

	// cgroup v2 spells "no limit" as the literal "max".
	raw := strings.TrimSpace(string(content))
	if raw == "max" {
		return 0, false
	}

	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 || limit >= cgroupMemoryUnlimited {
		return 0, false
	}

	return limit, true
}
