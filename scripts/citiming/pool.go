// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Cache-pool telemetry: the hourly snapshot of the repository's shared
// Actions cache pool. The pool monitor answers, from the cache API alone:
// how full the pool is, which cache kinds and workloads hold the bytes,
// which refs the entries belong to, and whether default-branch seeds are
// present and how old they are. Everything derives from the same
// collectCacheSnapshot used by the daily collection, so both views of the
// pool read identical data.

// Pool gauge names (part of the dashboard contract):
//
//   - ci.cache.pool.size_bytes, ci.cache.pool.entries: per cache_kind,
//     workload, os.platform and ref_type group.
//   - ci.cache.pool.total_bytes: the usage endpoint's active size.
//   - ci.cache.pool.limit_bytes: the storage-limit endpoint's quota, when
//     the API returns it (a 403 without admin rights is expected and
//     skips the gauge).
//   - ci.cache.pool.oldest_access_age_seconds: age of the least recently
//     used entry, the eviction horizon.
//   - ci.cache.pool.default_entry_age_seconds: age of the newest
//     default-branch entry per cache_kind, workload and os.platform, the
//     seed-presence signal.
const (
	poolMetricSize       = "ci.cache.pool.size_bytes"
	poolMetricEntries    = "ci.cache.pool.entries"
	poolMetricTotal      = "ci.cache.pool.total_bytes"
	poolMetricLimit      = "ci.cache.pool.limit_bytes"
	poolMetricOldest     = "ci.cache.pool.oldest_access_age_seconds"
	poolMetricDefaultAge = "ci.cache.pool.default_entry_age_seconds"

	giB = 1024 * 1024 * 1024
)

// poolNA is the workload of every entry that has no cloudx cache-key
// family.
const poolNA = "n/a"

// poolGroup is one aggregation cell of the pool: every entry is classified
// into exactly one group, and the size and entry gauges carry the group's
// tags. entries and sizeBytes are the accumulated values; the map key is
// poolKey so equal classifications merge.
type poolGroup struct {
	poolKey
	entries   int
	sizeBytes int64
}

// poolKey is the classification of an entry: the map key for aggregation.
type poolKey struct {
	kind       string
	workload   string
	osPlatform string
	refType    string
}

// poolCell is a (cache_kind, workload, os.platform) cell, used for the
// default-branch seed gauges.
type poolCell struct {
	kind       string
	workload   string
	osPlatform string
}

func poolGroupLess(a, b poolGroup) int {
	return strings.Compare(
		a.kind+"\x00"+a.workload+"\x00"+a.osPlatform+"\x00"+a.refType,
		b.kind+"\x00"+b.workload+"\x00"+b.osPlatform+"\x00"+b.refType)
}

func poolCellLess(a, b poolCell) int {
	return strings.Compare(
		a.kind+"\x00"+a.workload+"\x00"+a.osPlatform,
		b.kind+"\x00"+b.workload+"\x00"+b.osPlatform)
}

// poolDefaultGroup is one (cache_kind, workload, os.platform) cell of the
// default-branch seed presence gauge.
type poolDefaultGroup struct {
	poolCell
	ageSeconds float64
}

// poolReport is the classified snapshot every output format renders.
type poolReport struct {
	collectedAt     time.Time
	usage           cacheUsage
	entriesSumBytes int64
	groups          []poolGroup
	defaultGroup    []poolDefaultGroup
	// oldestAccessSeconds is the age of the least recently used entry;
	// zero with hasOldestAccess false when no entry carries an access
	// timestamp, in which case the horizon gauge is omitted instead of
	// reporting a misleading 0.
	oldestAccessSeconds float64
	hasOldestAccess     bool
	// limitBytes is the storage limit when the API returned it.
	limitBytes int64
	hasLimit   bool
	// limitNote explains a missing limit gauge.
	limitNote string
}

// goBuildKeyRe parses the cloudx save key
// go-cache-<os>-<arch>-ddtg-cx1-<family>-<digest16>-<go>-<branch>-<hash>-<run_id>.
// Family and branch may contain dashes (the branch may contain slashes),
// so the fixed-length hex segments and the numeric run ID anchor the
// match from both ends. The version segment allows one prerelease
// cluster (1.28.0-rc.1); a longer hyphen tail belongs to the branch.
var goBuildKeyRe = regexp.MustCompile(
	`^go-cache-(Linux|macOS|Windows)-(X64|ARM64)-ddtg-cx1-(.*)-([0-9a-f]{16})-` +
		`([0-9.]+[0-9A-Za-z.]*-[0-9A-Za-z.]+|[0-9.]+[0-9A-Za-z.]*)-(.*)-([0-9a-f]{40,64})-([0-9]+)$`)

// toolsKeyRe parses the tools-cache key <os>-<arch>-go-<version>-tools-<hash>.
var toolsKeyRe = regexp.MustCompile(`^(Linux|macOS|Windows)-(X64|ARM64)-go-.*-tools-`)

// setupGoBuiltinKeyRe parses the actions/setup-go built-in cache key,
// whose layout starts with the runner OS segment.
var setupGoBuiltinKeyRe = regexp.MustCompile(`^setup-go-(Linux|macOS|Windows)-`)

// poolOS maps a runner-os key segment to the os.platform tag value used by
// the cache-metrics payload, so the pool gauges join the per-restore
// series on the same tag values.
func poolOS(segment string) string {
	switch segment {
	case "Linux":
		return "linux"
	case "macOS":
		return "macos"
	case "Windows":
		return "windows"
	default:
		return "unknown"
	}
}

// classifyPoolKey classifies one cache key into cache_kind, workload and
// os.platform. Workload is the cloudx family of a go_build entry in the
// ddtg-cx1 layout; a go-cache key that predates the layout is still
// go_build but carries no workload.
func classifyPoolKey(key string) (kind, workload, osPlatform string) {
	switch {
	case strings.HasPrefix(key, "go-cache-"):
		if m := goBuildKeyRe.FindStringSubmatch(key); m != nil {
			// Group 3 is the family; group 4 is the 16-hex digest.
			return "go_build", m[3], poolOS(m[1])
		}
		return "go_build", poolNA, "unknown"
	case strings.HasPrefix(key, "setup-go-"):
		os := "unknown"
		if m := setupGoBuiltinKeyRe.FindStringSubmatch(key); m != nil {
			os = poolOS(m[1])
		}
		return "setup_go_builtin", poolNA, os
	case toolsKeyRe.MatchString(key):
		os := "unknown"
		if m := toolsKeyRe.FindStringSubmatch(key); m != nil {
			os = poolOS(m[1])
		}
		return "tools", poolNA, os
	// build-metrics.yml saves its own module cache under a
	// go-pkg-mod-build-metrics- prefix; it is not the AppSec module cache.
	case strings.HasPrefix(key, "go-pkg-mod-build-metrics-"):
		return "other", poolNA, "unknown"
	case strings.HasPrefix(key, "go-pkg-mod-"):
		return "appsec_modules", poolNA, "unknown"
	// system-tests.yml caches the npm-installed datadog-ci CLI.
	case strings.HasPrefix(key, "npm-datadog-ci"), strings.HasPrefix(key, "datadog-ci"):
		return "datadog_ci", poolNA, "unknown"
	case strings.HasPrefix(key, "gitdb"):
		return "gitdb", poolNA, "unknown"
	default:
		return "other", poolNA, "unknown"
	}
}

// classifyPoolRef classifies a cache ref. "default" is the repository's
// default branch, so seeded entries are visible independently of the
// branch name.
func classifyPoolRef(ref, defaultBranch string) string {
	switch {
	case ref != "" && ref == "refs/heads/"+defaultBranch:
		return "default"
	case strings.HasPrefix(ref, "refs/pull/"):
		return "pr"
	case strings.HasPrefix(ref, "refs/heads/mq-working-branch-"):
		return "mq"
	case strings.HasPrefix(ref, "refs/tags/"):
		return "release"
	default:
		return "other"
	}
}

// fetchDefaultBranch reads the repository's default branch for ref
// classification. A repo whose endpoint fails is classified against
// "main" rather than losing the whole snapshot.
func fetchDefaultBranch(c client) (string, error) {
	body, err := c.getJSON("repos/" + c.repoPath())
	if err != nil {
		return "", err
	}
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal([]byte(body), &repo); err != nil {
		return "", fmt.Errorf("decode repository: %w", err)
	}
	if repo.DefaultBranch == "" {
		return "", errors.New("repository response has no default_branch")
	}
	return repo.DefaultBranch, nil
}

// fetchStorageLimit reads the repository cache storage limit. The endpoint
// needs admin rights, so a 403 is an expected outcome: the caller reports
// it and the limit gauge is skipped.
func fetchStorageLimit(c client) (int64, error) {
	body, err := c.getJSON("repos/" + c.repoPath() + "/actions/cache/storage-limit")
	if err != nil {
		return 0, err
	}
	var limit struct {
		MaxCacheSizeGB *int64 `json:"max_cache_size_gb"`
	}
	if err := json.Unmarshal([]byte(body), &limit); err != nil {
		return 0, fmt.Errorf("decode storage limit: %w", err)
	}
	if limit.MaxCacheSizeGB == nil {
		return 0, errors.New("storage limit response has no max_cache_size_gb")
	}
	return *limit.MaxCacheSizeGB * giB, nil
}

// buildPoolReport classifies a snapshot into the gauge groups. The
// default-branch cells keep the newest entry's created_at per
// (cache_kind, workload, os.platform), so the seed gauge reports presence
// and freshness at once.
func buildPoolReport(snap cacheSnapshot, at time.Time, defaultBranch string) poolReport {
	groupSums := map[poolKey]poolGroup{}
	newestDefault := map[poolCell]time.Time{}
	var oldestAccess time.Time
	for _, e := range snap.Entries {
		kind, workload, osPlatform := classifyPoolKey(e.Key)
		refType := classifyPoolRef(e.Ref, defaultBranch)
		key := poolKey{kind: kind, workload: workload, osPlatform: osPlatform, refType: refType}
		g := groupSums[key]
		g.poolKey = key
		g.entries++
		g.sizeBytes += e.SizeInBytes
		groupSums[key] = g
		if created, err := parseTS(e.CreatedAt); err == nil && refType == "default" {
			cell := poolCell{kind: kind, workload: workload, osPlatform: osPlatform}
			if prev, ok := newestDefault[cell]; !ok || created.After(prev) {
				newestDefault[cell] = created
			}
		}
		if accessed, err := parseTS(e.LastAccessedAt); err == nil {
			if oldestAccess.IsZero() || accessed.Before(oldestAccess) {
				oldestAccess = accessed
			}
		}
	}
	report := poolReport{
		collectedAt: at,
		usage:       snap.Usage,
	}
	if !oldestAccess.IsZero() {
		report.oldestAccessSeconds = at.Sub(oldestAccess).Seconds()
		report.hasOldestAccess = true
	}
	for _, e := range snap.Entries {
		report.entriesSumBytes += e.SizeInBytes
	}
	report.groups = make([]poolGroup, 0, len(groupSums))
	for _, g := range groupSums {
		report.groups = append(report.groups, g)
	}
	slices.SortFunc(report.groups, poolGroupLess)
	for cell, newest := range newestDefault {
		report.defaultGroup = append(report.defaultGroup, poolDefaultGroup{
			poolCell:   cell,
			ageSeconds: at.Sub(newest).Seconds(),
		})
	}
	slices.SortFunc(report.defaultGroup, func(a, b poolDefaultGroup) int {
		return poolCellLess(a.poolCell, b.poolCell)
	})
	return report
}

// poolSeries is one Datadog v2 series.
type poolSeries struct {
	Metric string      `json:"metric"`
	Type   int         `json:"type"`
	Points []poolPoint `json:"points"`
	Tags   []string    `json:"tags"`
}

type poolPoint struct {
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

// poolPayload is the POST body for the Datadog /api/v2/series endpoint.
type poolPayload struct {
	Series []poolSeries `json:"series"`
}

// normalizeTag lowercases and replaces each run of disallowed characters
// with a single underscore, trimming leading and trailing ones, matching
// the cache-metrics payload builder so the pool gauges and the restore
// counts share tag spelling.
func normalizeTag(value string) string {
	var b strings.Builder
	pendingSep := false
	for _, r := range strings.ToLower(value) {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' ||
			r == '_' || r == '.' || r == '-'
		if !ok {
			// A run of disallowed characters collapses into one
			// separator, never at the start or the end.
			pendingSep = b.Len() > 0
			continue
		}
		if pendingSep {
			b.WriteByte('_')
			pendingSep = false
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// poolRepositoryTag renders the repository tag: the repository name
// without the owner, like the cache-metrics payload.
func poolRepositoryTag(repo string) string {
	_, name, _ := strings.Cut(repo, "/")
	return normalizeTag(name)
}

// buildPoolPayload renders the report as a Datadog series payload. Every
// series carries the repository tag; the group gauges add their
// classification tags.
func buildPoolPayload(r poolReport, repo string) poolPayload {
	ts := r.collectedAt.Unix()
	repository := poolRepositoryTag(repo)
	gauge := func(metric string, value float64, tags ...string) poolSeries {
		return poolSeries{
			Metric: metric,
			Type:   3,
			Points: []poolPoint{{Timestamp: ts, Value: value}},
			Tags:   append([]string{"repository:" + repository}, tags...),
		}
	}
	var series []poolSeries
	for _, g := range r.groups {
		tags := []string{
			"cache_kind:" + normalizeTag(g.kind),
			"workload:" + normalizeTag(g.workload),
			"os.platform:" + normalizeTag(g.osPlatform),
			"ref_type:" + normalizeTag(g.refType),
		}
		series = append(series,
			gauge(poolMetricSize, float64(g.sizeBytes), tags...),
			gauge(poolMetricEntries, float64(g.entries), tags...))
	}
	// The usage endpoint's active size is the authoritative total; the
	// listed entries can lag evictions.
	total := r.usage.ActiveCachesSizeInBytes
	if total == 0 {
		total = r.entriesSumBytes
	}
	series = append(series, gauge(poolMetricTotal, float64(total)))
	if r.hasLimit {
		series = append(series, gauge(poolMetricLimit, float64(r.limitBytes)))
	}
	if r.hasOldestAccess {
		series = append(series, gauge(poolMetricOldest, r.oldestAccessSeconds))
	}
	for _, g := range r.defaultGroup {
		series = append(series, gauge(poolMetricDefaultAge, g.ageSeconds,
			"cache_kind:"+normalizeTag(g.kind),
			"workload:"+normalizeTag(g.workload),
			"os.platform:"+normalizeTag(g.osPlatform)))
	}
	return poolPayload{Series: series}
}

// renderPoolText renders the human-readable snapshot summary.
func renderPoolText(r poolReport, repo string) string {
	limitNote := r.limitNote
	if !r.hasLimit && limitNote == "" {
		limitNote = "not returned by the API"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Cache pool snapshot for %s at %s\n", repo, r.collectedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "  usage endpoint: %d active caches, %.2f GiB\n",
		r.usage.ActiveCachesCount, float64(r.usage.ActiveCachesSizeInBytes)/giB)
	entries := 0
	for _, g := range r.groups {
		entries += g.entries
	}
	fmt.Fprintf(&b, "  listed entries: %d, summing %.2f GiB\n", entries, float64(r.entriesSumBytes)/giB)
	if r.hasLimit {
		fmt.Fprintf(&b, "  storage limit: %.2f GiB\n", float64(r.limitBytes)/giB)
	} else {
		fmt.Fprintf(&b, "  storage limit: unavailable (%s)\n", limitNote)
	}
	oldest := "n/a"
	if r.hasOldestAccess {
		oldest = fmtPoolAge(r.oldestAccessSeconds)
	}
	fmt.Fprintf(&b, "  oldest last access: %s\n", oldest)
	if len(r.defaultGroup) == 0 {
		b.WriteString("  default-branch entries: none\n")
	} else {
		b.WriteString("  default-branch entries (newest per kind/workload/os):\n")
		for _, g := range r.defaultGroup {
			fmt.Fprintf(&b, "    %-16s %-24s %-8s %10s old\n",
				g.kind, g.workload, g.osPlatform, fmtPoolAge(g.ageSeconds))
		}
	}
	b.WriteString("  by cache_kind/workload/os.platform/ref_type:\n")
	for _, g := range r.groups {
		fmt.Fprintf(&b, "    %-16s %-24s %-8s %-8s %3d entries %8.2f GiB\n",
			g.kind, g.workload, g.osPlatform, g.refType, g.entries, float64(g.sizeBytes)/giB)
	}
	return b.String()
}

// fmtPoolAge renders a duration for the text report.
func fmtPoolAge(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	d = max(d, 0)
	return d.Round(time.Minute).String()
}

// cmdPool prints one classified snapshot of the repository cache pool,
// either as text or as a Datadog series payload for the hourly monitor
// workflow to submit.
func cmdPool(args []string) error {
	flags := flagSet("pool")
	repo := flags.String("repo", "", "OWNER/NAME")
	format := flags.String("format", "text", "output format: text or datadog")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *repo == "" {
		return errors.New("pool needs --repo")
	}
	switch *format {
	case "text", "datadog":
	default:
		return fmt.Errorf("unknown --format %q (want text or datadog)", *format)
	}

	c := &ghClient{repo: *repo}
	snap, err := collectCacheSnapshot(c, nowUTC())
	if err != nil {
		return err
	}
	// Stamp the report AFTER collection: an entry created while the
	// snapshot paged through the API would otherwise carry a negative
	// age in the gauges.
	at := timeNow().UTC()
	defaultBranch, err := fetchDefaultBranch(c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ! default branch unavailable (%v); classifying refs against main\n", err)
		defaultBranch = "main"
	}
	report := buildPoolReport(snap, at, defaultBranch)
	if limit, err := fetchStorageLimit(c); err == nil {
		report.limitBytes, report.hasLimit = limit, true
	} else {
		report.limitNote = "storage-limit endpoint: " + err.Error()
		fmt.Fprintf(os.Stderr, "  ! storage limit unavailable: %v\n", err)
	}
	switch *format {
	case "text":
		fmt.Fprint(os.Stdout, renderPoolText(report, *repo))
	case "datadog":
		data, err := json.MarshalIndent(buildPoolPayload(report, *repo), "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, string(data))
	}
	return nil
}
