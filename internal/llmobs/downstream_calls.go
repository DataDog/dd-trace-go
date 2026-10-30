// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// maxDownstreamCalls caps the distinct endpoints recorded per tool span. Calls to further
// endpoints still count toward the totals and are reported in omitted_count.
const maxDownstreamCalls = 50

var downstreamIDSegment = regexp.MustCompile(`(?i)^(\d+|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[0-9a-f]{16,})$`)

// DownstreamCall describes one outbound client call (HTTP, database, gRPC) made while a tool
// span is open. Target must already be stripped of query strings and credentials.
type DownstreamCall struct {
	Kind     string
	Method   string
	Host     string
	Target   string
	Status   string
	Failed   bool
	Duration time.Duration
}

type downstreamCallEntry struct {
	call       DownstreamCall
	count      int
	errorCount int
	duration   time.Duration
}

// downstreamCalls has its own lock so client spans finishing on other goroutines never wait on
// the LLMObs span's lock, which is held while the span finishes.
type downstreamCalls struct {
	mu sync.Mutex
	// +checklocks:mu
	closed bool
	// +checklocks:mu
	entries []*downstreamCallEntry
	// +checklocks:mu
	index map[DownstreamCall]*downstreamCallEntry
	// +checklocks:mu
	omitted int
	// +checklocks:mu
	totalCount int
	// +checklocks:mu
	totalErrors int
	// +checklocks:mu
	totalDuration time.Duration
}

// maxDownstreamFieldLen bounds the host and target of each recorded call, so a long path from
// an untrusted server can't inflate the span event.
const maxDownstreamFieldLen = 256

// DownstreamPathTemplate replaces path segments that look like identifiers or secrets with "?":
// numeric, UUID and long hex IDs, segments containing "@" or "%" (emails, encoded values), and
// long segments mixing letters and digits (tokens).
func DownstreamPathTemplate(path string) string {
	segments := strings.Split(path, "/")
	for i, s := range segments {
		if isOpaqueSegment(s) {
			segments[i] = "?"
		}
	}
	if t := strings.Join(segments, "/"); t != "" {
		return t
	}
	return "/"
}

func isOpaqueSegment(s string) bool {
	if downstreamIDSegment.MatchString(s) || strings.ContainsAny(s, "@%") {
		return true
	}
	if len(s) < 24 {
		return false
	}
	var hasLetter, hasDigit bool
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsLetter(r):
			hasLetter = true
		}
	}
	return hasLetter && hasDigit
}

func truncateDownstreamField(s string) string {
	if len(s) <= maxDownstreamFieldLen {
		return s
	}
	cut := maxDownstreamFieldLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// downstreamEndpoint labels a call as "METHOD host/path", or "METHOD host target" when the target
// is not a path (database operations, gRPC methods), so host and target stay distinguishable.
func downstreamEndpoint(c DownstreamCall) string {
	hostTarget := c.Host + c.Target
	if c.Host != "" && c.Target != "" && !strings.HasPrefix(c.Target, "/") {
		hostTarget = c.Host + " " + c.Target
	}
	return strings.TrimSpace(c.Method + " " + hostTarget)
}

// RecordDownstreamCall adds c to the span's downstream call summary. Only tool spans keep one,
// and calls that arrive after the span finished are dropped.
func (s *Span) RecordDownstreamCall(c DownstreamCall) {
	if s == nil || s.spanKind != SpanKindTool {
		return
	}
	s.downstream.record(c)
}

func (d *downstreamCalls) record(c DownstreamCall) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.totalCount++
	if c.Failed {
		d.totalErrors++
	}
	d.totalDuration += c.Duration

	key := c
	key.Failed, key.Duration = false, 0
	key.Host, key.Target = truncateDownstreamField(key.Host), truncateDownstreamField(key.Target)
	entry, ok := d.index[key]
	if !ok {
		if len(d.entries) >= maxDownstreamCalls {
			d.omitted++
			return
		}
		if d.index == nil {
			d.index = make(map[DownstreamCall]*downstreamCallEntry)
		}
		entry = &downstreamCallEntry{call: key}
		d.index[key] = entry
		d.entries = append(d.entries, entry)
	}
	entry.count++
	if c.Failed {
		entry.errorCount++
	}
	entry.duration += c.Duration
}

func durationMs(d time.Duration) float64 {
	return math.Round(float64(d.Microseconds())) / 1e3
}

// close stops recording and returns the summary reported under metadata._dd.downstream_calls,
// or nil if no calls were recorded. The per-call list is for display; hosts, targets, statuses
// and endpoints are flat lists so spans can be filtered and grouped by them, and the totals are
// span-level scalars that aggregate correctly.
func (d *downstreamCalls) close() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	if d.totalCount == 0 {
		return nil
	}
	calls := make([]map[string]any, 0, len(d.entries))
	var hosts, targets, statuses, endpoints distinctStrings
	for _, e := range d.entries {
		c := e.call
		calls = append(calls, map[string]any{
			"kind":        c.Kind,
			"method":      c.Method,
			"host":        c.Host,
			"target":      c.Target,
			"status":      c.Status,
			"count":       e.count,
			"error_count": e.errorCount,
			"duration_ms": durationMs(e.duration),
		})
		hosts.add(c.Host)
		targets.add(c.Target)
		statuses.add(c.Status)
		endpoints.add(downstreamEndpoint(c))
	}
	summary := map[string]any{
		"calls":             calls,
		"hosts":             hosts.values,
		"targets":           targets.values,
		"statuses":          statuses.values,
		"endpoints":         endpoints.values,
		"total_count":       d.totalCount,
		"total_error_count": d.totalErrors,
		"total_duration_ms": durationMs(d.totalDuration),
	}
	if d.omitted > 0 {
		summary["omitted_count"] = d.omitted
	}
	return summary
}

type distinctStrings struct {
	seen   map[string]struct{}
	values []string
}

func (d *distinctStrings) add(v string) {
	if v == "" {
		return
	}
	if _, ok := d.seen[v]; ok {
		return
	}
	if d.seen == nil {
		d.seen = make(map[string]struct{})
	}
	d.seen[v] = struct{}{}
	d.values = append(d.values, v)
}
