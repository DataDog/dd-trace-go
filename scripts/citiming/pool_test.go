// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// poolEntryJSON builds one /actions/caches entry.
func poolEntryJSON(id int64, key, ref string, size int64, created, accessed string) string {
	entry := map[string]any{
		"id":               id,
		"key":              key,
		"ref":              ref,
		"size_in_bytes":    size,
		"created_at":       created,
		"last_accessed_at": accessed,
	}
	b, err := json.Marshal(entry)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// poolCachesPage wraps entries in the list response.
func poolCachesPage(entries ...string) string {
	return `{"total_count":` + strconv.Itoa(len(entries)) + `,"actions_caches":[` +
		strings.Join(entries, ",") + `]}`
}

func TestClassifyPoolKeyEveryKind(t *testing.T) {
	cases := []struct {
		key                 string
		kind, workload, osX string
	}{
		// go_build in the ddtg-cx1 layout: the family carries dashes and
		// the branch carries slashes, so only the fixed-length segments
		// can anchor the split.
		{
			key: "go-cache-Linux-X64-ddtg-cx1-otelc-external-app-c3177be3f8c1532f-1.26.4-eliottness/appsec-disable-features-" +
				"60e668c987fcc72f27a63db16f95cea76a22700c35ae41b32482e8e04c9f6380-38403036804",
			kind: "go_build", workload: "otelc-external-app", osX: "linux",
		},
		{
			key: "go-cache-Windows-ARM64-ddtg-cx1-multi-os-314198ec86eada43-1.27.2-main-" +
				"60e668c987fcc72f27a63db16f95cea76a22700c35ae41b32482e8e04c9f6380-38402253744",
			kind: "go_build", workload: "multi-os", osX: "windows",
		},
		{
			// Prerelease versions carry one hyphen cluster; the branch
			// must still parse after it.
			key: "go-cache-Windows-X64-ddtg-cx1-multi-os-314198ec86eada43-1.28.0-rc.1-main-" +
				"60e668c987fcc72f27a63db16f95cea76a22700c35ae41b32482e8e04c9f6380-38402253744",
			kind: "go_build", workload: "multi-os", osX: "windows",
		},
		{
			key: "go-cache-macOS-ARM64-ddtg-cx1-unit-contrib-92572337d046055a-1.27.2-" +
				"brian.marks/otel-deployment-environment-name-" +
				"60e668c987fcc72f27a63db16f95cea76a22700c35ae41b32482e8e04c9f6380-38402253726",
			kind: "go_build", workload: "unit-contrib", osX: "macos",
		},
		// A go-cache key that predates the layout: still go_build, but no
		// workload and no OS.
		{
			key:      "go-cache-Linux-X64-legacy-key",
			kind:     "go_build",
			workload: poolNA,
			osX:      "unknown",
		},
		{
			key:      "Linux-X64-go-1.27.2-tools-d4bfd7eb5342b83995581b2c2ce57bb146e8d0a05d9d08001b3f70c26a26cd72",
			kind:     "tools",
			workload: poolNA, osX: "linux",
		},
		{
			key:      "macOS-ARM64-go-1.26.9-tools-d4bfd7eb5342b83995581b2c2ce57bb146e8d0a05d9d08001b3f70c26a26cd72",
			kind:     "tools",
			workload: poolNA, osX: "macos",
		},
		{
			key:      "setup-go-Linux-x64-ubuntu24-go-1.27.2-4458b2cfb4279ca8a567742f397e66a22a0dfdee2362894f1a7b51cf0809c0b4",
			kind:     "setup_go_builtin",
			workload: poolNA, osX: "linux",
		},
		{
			key:      "setup-go-Windows-x64-windows11-go-1.26.9-4458b2cfb4279ca8a567742f397e66a22a0dfdee2362894f1a7b51cf0809c0b4",
			kind:     "setup_go_builtin",
			workload: poolNA, osX: "windows",
		},
		{
			key:  "go-pkg-mod-f6762d137b5e43d204c8afcb41b910685777f80c7feb64a2771c1ed664283657",
			kind: "appsec_modules", workload: poolNA, osX: "unknown",
		},
		{
			key: "datadog-ci-Linux-X64-v5.25.0", kind: "datadog_ci", workload: poolNA, osX: "unknown",
		},
		{
			key: "datadog-ci-cli-linux-x64", kind: "datadog_ci", workload: poolNA, osX: "unknown",
		},
		{
			// system-tests.yml caches the npm-installed datadog-ci CLI.
			key: "npm-datadog-ci-Linux-v1", kind: "datadog_ci", workload: poolNA, osX: "unknown",
		},
		{
			// build-metrics.yml saves its own module cache under this
			// prefix; it must not inflate the AppSec gauges.
			key:  "go-pkg-mod-build-metrics-60e668c987fcc72f27a63db16f95cea76a22700c35ae41b32482e8e04c9f6380",
			kind: "other", workload: poolNA, osX: "unknown",
		},
		{
			key: "gitdb-67680905-5440838bca4e3081ddebbac1ec98a4244e4ec695", kind: "gitdb", workload: poolNA, osX: "unknown",
		},
		{
			key: "gitdb-system-tests-abc123", kind: "gitdb", workload: poolNA, osX: "unknown",
		},
		{
			key:  "runner_venv-Linux-/opt/hostedtoolcache/Python/3.12.15/x64-a0c2b8ee35d7fa6fe4d859507e829e12ebb6e073b3401b8690b94c9ec3aefd6d",
			kind: "other", workload: poolNA, osX: "unknown",
		},
	}
	for _, c := range cases {
		kind, workload, osPlatform := classifyPoolKey(c.key)
		if kind != c.kind || workload != c.workload || osPlatform != c.osX {
			t.Errorf("classifyPoolKey(%q) = (%q, %q, %q), want (%q, %q, %q)",
				c.key, kind, workload, osPlatform, c.kind, c.workload, c.osX)
		}
	}
}

func TestClassifyPoolRef(t *testing.T) {
	cases := []struct {
		ref, defaultBranch, want string
	}{
		{"refs/heads/main", "main", "default"},
		{"refs/heads/develop", "develop", "default"},
		// A default branch that is not this repo's is not "default".
		{"refs/heads/feature", "main", "other"},
		{"refs/heads/main", "develop", "other"},
		{"refs/pull/5569/merge", "main", "pr"},
		{"refs/pull/1/head", "main", "pr"},
		{"refs/heads/mq-working-branch-abc123", "main", "mq"},
		{"refs/tags/v2.11.0", "main", "release"},
		{"refs/heads/release-v2.11.0", "main", "release"},
		{"refs/heads/release-v2", "main", "release"},
		{"", "main", "other"},
	}
	for _, c := range cases {
		if got := classifyPoolRef(c.ref, c.defaultBranch); got != c.want {
			t.Errorf("classifyPoolRef(%q, %q) = %q, want %q", c.ref, c.defaultBranch, got, c.want)
		}
	}
}

// poolSnapshotFixture returns a snapshot covering the group and default
// gauges: one seeded multi-OS entry on main, two PR go_build entries of
// the same family, one tools entry, and an appsec entry.
func poolSnapshotFixture() cacheSnapshot {
	return cacheSnapshot{
		Kind: "cache_snapshot", Schema: 2, CollectedAt: "2026-10-13T12:00:00Z",
		Usage: cacheUsage{ActiveCachesSizeInBytes: 4 * giB, ActiveCachesCount: 5},
		Entries: []cacheEntry{
			{
				ID: 1, Key: "go-cache-Windows-X64-ddtg-cx1-multi-os-314198ec86eada43-1.27.2-main-" +
					"60e668c987fcc72f27a63db16f95cea76a22700c35ae41b32482e8e04c9f6380-38402253744",
				Ref: "refs/heads/main", SizeInBytes: 2 * giB,
				CreatedAt: "2026-10-13T06:00:00Z", LastAccessedAt: "2026-10-13T10:00:00Z",
			},
			{
				ID: 2, Key: "go-cache-Windows-X64-ddtg-cx1-multi-os-314198ec86eada43-1.27.2-branch-" +
					"60e668c987fcc72f27a63db16f95cea76a22700c35ae41b32482e8e04c9f6380-38403001",
				Ref: "refs/pull/5570/merge", SizeInBytes: 1 * giB,
				CreatedAt: "2026-10-13T11:00:00Z", LastAccessedAt: "2026-10-13T11:00:00Z",
			},
			{
				ID: 3, Key: "go-cache-Windows-X64-ddtg-cx1-multi-os-314198ec86eada43-1.26.9-branch-" +
					"60e668c987fcc72f27a63db16f95cea76a22700c35ae41b32482e8e04c9f6380-38403002",
				Ref: "refs/pull/5570/merge", SizeInBytes: 500,
				CreatedAt: "2026-10-13T11:30:00Z", LastAccessedAt: "2026-10-13T09:00:00Z",
			},
			{
				ID: 4, Key: "Linux-X64-go-1.27.2-tools-d4bfd7eb5342b83995581b2c2ce57bb146e8d0a05d9d08001b3f70c26a26cd72",
				Ref: "refs/heads/mq-working-branch-x", SizeInBytes: 86 * 1024 * 1024,
				CreatedAt: "2026-10-13T08:00:00Z", LastAccessedAt: "2026-10-13T08:00:00Z",
			},
			{
				ID: 5, Key: "go-pkg-mod-f6762d137b5e43d204c8afcb41b910685777f80c7feb64a2771c1ed664283657",
				Ref: "refs/heads/main", SizeInBytes: 1292 * 1024 * 1024,
				CreatedAt: "2026-10-12T06:00:00Z", LastAccessedAt: "2026-10-13T07:00:00Z",
			},
		},
	}
}

func TestBuildPoolReportGroups(t *testing.T) {
	at := mustTime(t, "2026-10-13T12:00:00Z")
	report := buildPoolReport(poolSnapshotFixture(), at, "main")

	find := func(kind, workload, osX, refType string) *poolGroup {
		for i, g := range report.groups {
			if g.kind == kind && g.workload == workload && g.osPlatform == osX && g.refType == refType {
				return &report.groups[i]
			}
		}
		return nil
	}
	if g := find("go_build", "multi-os", "windows", "default"); g == nil || g.entries != 1 || g.sizeBytes != 2*giB {
		t.Errorf("seed group = %+v, want one 2 GiB entry", g)
	}
	if g := find("go_build", "multi-os", "windows", "pr"); g == nil || g.entries != 2 || g.sizeBytes != 1*giB+500 {
		t.Errorf("pr group = %+v, want two entries of 1 GiB + 500 B", g)
	}
	// The 1.26 entry is a separate default cell only if it were on main;
	// here it is PR-scoped, so only the appsec entry joins the default
	// cells with the multi-OS seed.
	if len(report.defaultGroup) != 2 {
		t.Fatalf("default groups = %+v, want the multi-os seed and appsec_modules", report.defaultGroup)
	}
	var seed, appsec *poolDefaultGroup
	for i, g := range report.defaultGroup {
		if g.kind == "appsec_modules" {
			appsec = &report.defaultGroup[i]
		}
		if g.kind == "go_build" && g.workload == "multi-os" && g.osPlatform == "windows" {
			seed = &report.defaultGroup[i]
		}
	}
	if seed == nil || appsec == nil {
		t.Fatalf("default groups missing seed or appsec cells: %+v", report.defaultGroup)
	}
	if want := 6 * 3600.0; seed.ageSeconds != want {
		t.Errorf("seed age = %f, want %f (newest default multi-os entry)", seed.ageSeconds, want)
	}
	if want := 30 * 3600.0; appsec.ageSeconds != want {
		t.Errorf("appsec default age = %f, want %f", appsec.ageSeconds, want)
	}
	// Oldest access: 07:00 across the entries (the appsec entry).
	if want := 5 * 3600.0; report.oldestAccessSeconds != want {
		t.Errorf("oldest access = %f, want %f", report.oldestAccessSeconds, want)
	}
}

func TestBuildPoolPayloadTagsAndLimit(t *testing.T) {
	at := mustTime(t, "2026-10-13T12:00:00Z")
	report := buildPoolReport(poolSnapshotFixture(), at, "main")
	report.limitBytes, report.hasLimit = 10*giB, true
	payload := buildPoolPayload(report, "DataDog/dd-trace-go")

	metrics := map[string][]poolSeries{}
	for _, s := range payload.Series {
		metrics[s.Metric] = append(metrics[s.Metric], s)
	}
	if len(metrics[poolMetricSize]) != len(report.groups) {
		t.Errorf("%s series = %d, want %d groups", poolMetricSize,
			len(metrics[poolMetricSize]), len(report.groups))
	}
	if len(metrics[poolMetricEntries]) != len(report.groups) {
		t.Errorf("%s series = %d, want %d groups", poolMetricEntries,
			len(metrics[poolMetricEntries]), len(report.groups))
	}
	if len(metrics[poolMetricTotal]) != 1 {
		t.Fatalf("%s series = %d, want 1", poolMetricTotal, len(metrics[poolMetricTotal]))
	}
	if got := metrics[poolMetricTotal][0].Points[0].Value; int64(got) != 4*giB {
		t.Errorf("total = %d, want the usage endpoint's 4 GiB", int64(got))
	}
	if len(metrics[poolMetricLimit]) != 1 || metrics[poolMetricLimit][0].Points[0].Value != float64(10*giB) {
		t.Errorf("%s series = %+v, want one 10 GiB gauge", poolMetricLimit, metrics[poolMetricLimit])
	}
	// Every series carries the repository tag and the group series carry
	// the classification tags.
	for _, s := range payload.Series {
		if !containsTag(s.Tags, "repository:dd-trace-go") {
			t.Errorf("%s lacks repository tag: %v", s.Metric, s.Tags)
		}
	}
	var seedSize poolSeries
	found := false
	for _, s := range metrics[poolMetricSize] {
		if containsTag(s.Tags, "cache_kind:go_build") && containsTag(s.Tags, "workload:multi-os") &&
			containsTag(s.Tags, "os.platform:windows") && containsTag(s.Tags, "ref_type:default") {
			seedSize, found = s, true
		}
	}
	if !found {
		t.Fatalf("no seed size series: %+v", metrics[poolMetricSize])
	}
	if seedSize.Points[0].Value != float64(2*giB) {
		t.Errorf("seed size = %f, want 2 GiB", seedSize.Points[0].Value)
	}
	if seedSize.Points[0].Timestamp != at.Unix() {
		t.Errorf("seed timestamp = %d, want %d", seedSize.Points[0].Timestamp, at.Unix())
	}
	// The default-age gauge joins the seed's classification minus ref_type.
	var defaultAge []poolSeries
	for _, s := range metrics[poolMetricDefaultAge] {
		if containsTag(s.Tags, "workload:multi-os") && containsTag(s.Tags, "os.platform:windows") {
			defaultAge = append(defaultAge, s)
		}
	}
	if len(defaultAge) != 1 {
		t.Fatalf("default-age seed series = %d, want 1", len(defaultAge))
	}
	if got := defaultAge[0].Points[0].Value; got != 6*3600 {
		t.Errorf("default-age value = %f, want 21600", got)
	}
}

func containsTag(tags []string, want string) bool {
	return slices.Contains(tags, want)
}

func TestBuildPoolPayloadWithoutLimit(t *testing.T) {
	at := mustTime(t, "2026-10-13T12:00:00Z")
	report := buildPoolReport(poolSnapshotFixture(), at, "main")
	report.limitNote = "storage-limit endpoint: 403"
	payload := buildPoolPayload(report, "DataDog/dd-trace-go")
	for _, s := range payload.Series {
		if s.Metric == poolMetricLimit {
			t.Errorf("limit series present although the API returned none")
		}
	}
	text := renderPoolText(report, "DataDog/dd-trace-go")
	if !strings.Contains(text, "storage limit: unavailable") {
		t.Errorf("text report does not report the missing limit:\n%s", text)
	}
	// The total falls back to the listed entries when the usage endpoint
	// reports nothing.
	report.usage = cacheUsage{}
	payload = buildPoolPayload(report, "DataDog/dd-trace-go")
	var total float64
	for _, s := range payload.Series {
		if s.Metric == poolMetricTotal {
			total = s.Points[0].Value
		}
	}
	var sum int64
	for _, e := range poolSnapshotFixture().Entries {
		sum += e.SizeInBytes
	}
	if int64(total) != sum {
		t.Errorf("fallback total = %d, want the listed sum %d", int64(total), sum)
	}
}

func TestPoolTagHelpers(t *testing.T) {
	if got := poolRepositoryTag("DataDog/dd-trace-go"); got != "dd-trace-go" {
		t.Errorf("repository tag = %q, want dd-trace-go", got)
	}
	if got := normalizeTag("Main Branch Tests!"); got != "main_branch_tests" {
		t.Errorf("normalizeTag = %q, want main_branch_tests", got)
	}
	if got := normalizeTag("--n/a--"); got != "--n_a--" {
		// Dashes are allowed characters, exactly like payload.sh's sed;
		// only non-[a-z0-9_.-] runs collapse into underscores.
		t.Errorf("normalizeTag = %q, want --n_a--", got)
	}
	if got := normalizeTag("!!!"); got != "unknown" {
		t.Errorf("normalizeTag = %q, want unknown", got)
	}
}

func TestFetchStorageLimitAndDefaultBranch(t *testing.T) {
	fake := &fakeClient{repo: "DataDog/dd-trace-go", defaultBranch: "main"}
	branch, err := fetchDefaultBranch(fake)
	if err != nil || branch != "main" {
		t.Fatalf("default branch = %q, %v; want main", branch, err)
	}
	// Without admin rights the endpoint 403s and the gauge is skipped.
	if _, err := fetchStorageLimit(fake); err == nil {
		t.Fatal("storage limit fetched although the endpoint 403s")
	}
	fake.storageLimit = `{"max_cache_size_gb":10}`
	limit, err := fetchStorageLimit(fake)
	if err != nil || limit != 10*giB {
		t.Fatalf("storage limit = %d, %v; want 10 GiB", limit, err)
	}
	fake.storageLimit = `{}`
	if _, err := fetchStorageLimit(fake); err == nil {
		t.Fatal("empty storage limit response accepted")
	}
}

func TestPoolTextRendersSeedPresence(t *testing.T) {
	at := mustTime(t, "2026-10-13T12:00:00Z")
	report := buildPoolReport(poolSnapshotFixture(), at, "main")
	text := renderPoolText(report, "DataDog/dd-trace-go")
	for _, want := range []string{
		"Cache pool snapshot for DataDog/dd-trace-go",
		"usage endpoint: 5 active caches, 4.00 GiB",
		"oldest last access: 5h0m0s",
		"go_build         multi-os                 windows  default    1 entries     2.00 GiB",
		"default-branch entries (newest per kind/workload/os):",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text report lacks %q:\n%s", want, text)
		}
	}
	empty := buildPoolReport(cacheSnapshot{CollectedAt: "2026-10-13T12:00:00Z"}, at, "main")
	if text := renderPoolText(empty, "DataDog/dd-trace-go"); !strings.Contains(text, "default-branch entries: none") {
		t.Errorf("empty pool report does not say no default-branch entries:\n%s", text)
	}
}

func TestPoolEntriesListedThroughCollectCacheSnapshot(t *testing.T) {
	entries := []string{
		poolEntryJSON(7, "gitdb-1-abc", "refs/pull/1/merge", 1024, "2026-10-13T10:00:00Z", "2026-10-13T10:00:00Z"),
	}
	fake := &fakeClient{
		repo:          "DataDog/dd-trace-go",
		defaultBranch: "main",
		usage:         `{"active_caches_size_in_bytes":1024,"active_caches_count":1}`,
		cachesJSON:    poolCachesPage(entries...),
	}
	snap, err := collectCacheSnapshot(fake, "2026-10-13T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Entries) != 1 || snap.Entries[0].Key != "gitdb-1-abc" {
		t.Fatalf("snapshot entries = %+v", snap.Entries)
	}
	report := buildPoolReport(snap, mustTime(t, "2026-10-13T12:00:00Z"), "main")
	if len(report.groups) != 1 || report.groups[0].kind != "gitdb" || report.groups[0].refType != "pr" {
		t.Fatalf("report groups = %+v", report.groups)
	}
}

func TestPoolDeterministicOrdering(t *testing.T) {
	at := mustTime(t, "2026-10-13T12:00:00Z")
	first := buildPoolPayload(buildPoolReport(poolSnapshotFixture(), at, "main"), "DataDog/dd-trace-go")
	second := buildPoolPayload(buildPoolReport(poolSnapshotFixture(), at, "main"), "DataDog/dd-trace-go")
	a, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Error("two identical snapshots produced different payload orderings")
	}
}

func TestFmtPoolAge(t *testing.T) {
	cases := []struct {
		seconds float64
		want    string
	}{
		{90 * 60, "1h30m0s"},
		{3600, "1h0m0s"},
		{30 * 3600, "30h0m0s"},
		{-5, "0s"},
	}
	for _, c := range cases {
		if got := fmtPoolAge(c.seconds); got != c.want {
			t.Errorf("fmtPoolAge(%f) = %q, want %q", c.seconds, got, c.want)
		}
	}
}

func TestPoolOmitsEvictionHorizonWhenNoAccessFound(t *testing.T) {
	at := mustTime(t, "2026-10-13T12:00:00Z")
	entries := []string{
		poolEntryJSON(9, "go-cache-Windows-X64-ddtg-cx1-multi-os-314198ec86eada43-1.27.2-main-"+
			"60e668c987fcc72f27a63db16f95cea76a22700c35ae41b32482e8e04c9f6380-38402253744",
			"refs/heads/main", giB, "2026-10-13T06:00:00Z", ""),
	}
	fake := &fakeClient{
		repo:          "DataDog/dd-trace-go",
		defaultBranch: "main",
		cachesJSON:    poolCachesPage(entries...),
	}
	snap, err := collectCacheSnapshot(fake, "2026-10-13T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	report := buildPoolReport(snap, at, "main")
	payload := buildPoolPayload(report, "DataDog/dd-trace-go")
	for _, s := range payload.Series {
		if s.Metric == poolMetricOldest {
			t.Error("eviction horizon emitted although no entry carries an access timestamp")
		}
	}
	if text := renderPoolText(report, "DataDog/dd-trace-go"); !strings.Contains(text, "oldest last access: n/a") {
		t.Errorf("text report does not mark the missing horizon:\n%s", text)
	}
}

func TestCollectCacheSnapshotDedupesPagingShifts(t *testing.T) {
	// The list endpoint pages without a stable sort; an entry can repeat
	// across a page boundary while the pool churns. The snapshot must
	// keep each entry once.
	h := "60e668c987fcc72f27a63db16f95cea76a22700c35ae41b32482e8e04c9f6380"
	page := make([]string, 0, 3)
	page = append(page,
		poolEntryJSON(11, "gitdb-1-abc", "refs/pull/1/merge", 1024, "2026-10-13T10:00:00Z", "2026-10-13T10:00:00Z"),
		poolEntryJSON(12, "go-cache-Linux-X64-ddtg-cx1-unit-core-c4dca83bdf2018a1-1.27.2-main-"+h+"-1",
			"refs/heads/main", giB, "2026-10-13T06:00:00Z", "2026-10-13T10:00:00Z"),
		// Entry 12 repeated, as the next page would after the inventory
		// shifted mid-paging; only the key differs so the dedup cannot
		// rely on the entry content.
		poolEntryJSON(12, "go-cache-Linux-X64-ddtg-cx1-unit-core-c4dca83bdf2018a1-1.27.2-main-"+h+"-2",
			"refs/heads/main", 2*giB, "2026-10-13T06:00:00Z", "2026-10-13T10:00:00Z"))
	fake := &fakeClient{
		repo:          "DataDog/dd-trace-go",
		defaultBranch: "main",
		cachesJSON:    poolCachesPage(page...),
	}
	snap, err := collectCacheSnapshot(fake, "2026-10-13T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Entries) != 2 {
		t.Fatalf("snapshot kept %d entries, want 2 (the repeated ID dropped)", len(snap.Entries))
	}
}
