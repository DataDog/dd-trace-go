// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"net/url"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// FlagSelection limits the flags a provider loads to those that have any of
// Tags or whose key matches any of KeyPatterns. The server applies the
// selection, so the provider downloads and holds only the selected flags.
// Evaluating a flag outside the selection returns the default value with
// FLAG_NOT_FOUND.
//
// Each value is trimmed of leading and trailing Unicode white space. Empty
// values and duplicates are dropped. A selection that is empty after that
// loads every flag.
//
// Each list holds at most 31 values, and each value is at most 200 UTF-8
// bytes. A selection over a limit, with a value that is not valid UTF-8, or
// that makes the request URL longer than 8192 bytes is not sent: the provider
// logs an error and loads every flag. When the server cannot apply a
// selection, it returns every flag and the provider logs an error once per
// process per reason. A selection that matches no flag is not an error: the
// provider becomes ready with no flags.
//
// Set it through ProviderConfig.FlagSelection; there is no environment
// variable. Each provider copies its one selection at construction. Under
// Remote Config the selection is ignored with a warning. LLM Observability
// prompt flags share the selection of the default provider.
type FlagSelection struct {
	// Tags selects flags that have any of these tags. Tags match exactly and
	// are case-sensitive.
	Tags []string
	// KeyPatterns selects flags whose whole key matches any of these
	// patterns, case-sensitively. A * matches any run of characters,
	// including none. Every other character is literal.
	KeyPatterns []string
}

// The value limits are the server's, shared by every Datadog SDK:
// https://github.com/DataDog/ffe-system-test-data/blob/main/flag-selection/README.md
const (
	flagSelectionMaxValues          = 31
	flagSelectionMaxTagBytes        = 200
	flagSelectionMaxKeyPatternBytes = 200
	// Fastly answers URLs over 8 KB with 414 URI Too Long:
	// https://docs.fastly.com/products/network-services-resource-limits
	flagSelectionMaxURLBytes = 8192
)

const (
	flagSelectionIgnoredHeader = "DD-Flag-Selection-Ignored"
	flagSelectionDroppedMetric = "flag_selection.dropped"
	flagSelectionIgnoredMetric = "flag_selection.ignored"
)

type flagSelectionDrop uint8

const (
	flagSelectionNotDropped flagSelectionDrop = iota
	flagSelectionMalformed
	flagSelectionTooManyValues
	flagSelectionInvalidKeyPattern
	flagSelectionInvalidTag
	flagSelectionURLTooLong
)

// reason is the telemetry reason, from the set the server reports in
// DD-Flag-Selection-Ignored.
func (d flagSelectionDrop) reason() string {
	switch d {
	case flagSelectionMalformed:
		return "malformed_selection"
	case flagSelectionTooManyValues, flagSelectionURLTooLong:
		return "too_many_values"
	case flagSelectionInvalidKeyPattern:
		return "invalid_key_pattern"
	case flagSelectionInvalidTag:
		return "invalid_tag"
	default:
		return ""
	}
}

type canonicalFlagSelection struct {
	tags        []string
	keyPatterns []string
}

func (s canonicalFlagSelection) empty() bool {
	return len(s.tags) == 0 && len(s.keyPatterns) == 0
}

// canonicalizeFlagSelection validates s and returns its canonical lists, in
// new slices. The checks run in the server's order of precedence.
func canonicalizeFlagSelection(s *FlagSelection) (canonicalFlagSelection, flagSelectionDrop) {
	if s == nil {
		return canonicalFlagSelection{}, flagSelectionNotDropped
	}
	if !allValidUTF8(s.Tags) || !allValidUTF8(s.KeyPatterns) {
		return canonicalFlagSelection{}, flagSelectionMalformed
	}
	c := canonicalFlagSelection{
		tags:        canonicalFlagSelectionList(s.Tags),
		keyPatterns: canonicalFlagSelectionList(s.KeyPatterns),
	}
	switch {
	case len(c.tags) > flagSelectionMaxValues || len(c.keyPatterns) > flagSelectionMaxValues:
		return canonicalFlagSelection{}, flagSelectionTooManyValues
	case slices.ContainsFunc(c.keyPatterns, func(p string) bool { return len(p) > flagSelectionMaxKeyPatternBytes }):
		return canonicalFlagSelection{}, flagSelectionInvalidKeyPattern
	case slices.ContainsFunc(c.tags, func(t string) bool { return len(t) > flagSelectionMaxTagBytes }):
		return canonicalFlagSelection{}, flagSelectionInvalidTag
	}
	return c, flagSelectionNotDropped
}

func allValidUTF8(values []string) bool {
	return !slices.ContainsFunc(values, func(v string) bool { return !utf8.ValidString(v) })
}

// canonicalFlagSelectionList trims each value of Unicode White_Space, which is
// what strings.TrimSpace removes, drops empty values and duplicates, and sorts
// the rest by UTF-8 bytes.
func canonicalFlagSelectionList(values []string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// appendFlagSelection returns endpoint with s appended to its query, and false
// when the result would be longer than flagSelectionMaxURLBytes.
func appendFlagSelection(endpoint string, s canonicalFlagSelection) (string, bool) {
	u, err := url.Parse(endpoint)
	if err != nil {
		// Not expected: endpoint is the String() of a parsed URL.
		return endpoint, false
	}
	var query strings.Builder
	query.WriteString(u.RawQuery)
	for _, tag := range s.tags {
		appendFlagSelectionParam(&query, "selected_flag_tags", tag)
	}
	for _, pattern := range s.keyPatterns {
		appendFlagSelectionParam(&query, "selected_flag_key_patterns", pattern)
	}
	u.RawQuery = query.String()
	withSelection := u.String()
	if len(withSelection) > flagSelectionMaxURLBytes {
		return endpoint, false
	}
	return withSelection, true
}

// appendFlagSelectionParam percent-encodes value per RFC 3986: QueryEscape
// leaves only the unreserved characters as they are, but writes a space as +.
func appendFlagSelectionParam(query *strings.Builder, name, value string) {
	if query.Len() > 0 {
		query.WriteByte('&')
	}
	query.WriteString(name)
	query.WriteByte('=')
	query.WriteString(strings.ReplaceAll(url.QueryEscape(value), "+", "%20"))
}

// requestURLWithFlagSelection returns the request URL for endpoint and s, and
// whether it carries the selection. It reports a selection it cannot send.
func requestURLWithFlagSelection(endpoint string, s *FlagSelection) (string, bool) {
	selection, drop := canonicalizeFlagSelection(s)
	if drop == flagSelectionNotDropped {
		if selection.empty() {
			return endpoint, false
		}
		if withSelection, ok := appendFlagSelection(endpoint, selection); ok {
			return withSelection, true
		}
		drop = flagSelectionURLTooLong
	}
	reportFlagSelectionDropped(drop)
	return endpoint, false
}

// flagSelectionReported holds the diagnostics already reported in this
// process, keyed by "metric:reason" or "remote_config". Every provider shares it.
var flagSelectionReported sync.Map

func firstFlagSelectionReport(key string) bool {
	_, reported := flagSelectionReported.LoadOrStore(key, struct{}{})
	return !reported
}

func countFlagSelection(metric, reason string) {
	telemetry.Count(telemetry.NamespaceTracers, metric, []string{"reason:" + reason}).Submit(1)
}

// reportFlagSelectionDropped logs every drop but counts each reason once per
// process. Each reason has its own log format string: log.Error groups lines
// by format string, so a shared one would hide all but the first reason.
func reportFlagSelectionDropped(drop flagSelectionDrop) {
	containLoggerPanic(func() {
		switch drop {
		case flagSelectionMalformed:
			log.Error("openfeature: ProviderConfig.FlagSelection was not sent (malformed_selection: ProviderConfig.FlagSelection must have optional Tags and KeyPatterns slices of valid UTF-8 strings). Loading the full flag configuration.") //errtrack:ignore invalid user configuration
		case flagSelectionTooManyValues:
			log.Error("openfeature: ProviderConfig.FlagSelection was not sent (too_many_values: each list accepts at most 31 values). Loading the full flag configuration.") //errtrack:ignore invalid user configuration
		case flagSelectionInvalidKeyPattern:
			log.Error("openfeature: ProviderConfig.FlagSelection was not sent (invalid_key_pattern: each key pattern is at most 200 UTF-8 bytes). Loading the full flag configuration.") //errtrack:ignore invalid user configuration
		case flagSelectionInvalidTag:
			log.Error("openfeature: ProviderConfig.FlagSelection was not sent (invalid_tag: each tag is at most 200 UTF-8 bytes). Loading the full flag configuration.") //errtrack:ignore invalid user configuration
		case flagSelectionURLTooLong:
			log.Error("openfeature: ProviderConfig.FlagSelection was not sent (too_many_values: the request URL is at most 8192 bytes). Loading the full flag configuration.") //errtrack:ignore invalid user configuration
		}
	})
	if firstFlagSelectionReport(flagSelectionDroppedMetric + ":" + drop.reason()) {
		countFlagSelection(flagSelectionDroppedMetric, drop.reason())
	}
}

// sanitizeFlagSelectionIgnoredReason keeps a byte-exact known reason and
// reports anything else as unknown, so a header value never reaches a log line
// or a metric tag.
func sanitizeFlagSelectionIgnoredReason(header string) string {
	switch header {
	case "malformed_selection", "too_many_values", "invalid_key_pattern", "invalid_tag", "tag_load_failed":
		return header
	default:
		return "unknown"
	}
}

// reportFlagSelectionIgnored logs and counts each reason once per process: a
// provider polls for its whole lifetime, and the server repeats the header on
// every full response.
func reportFlagSelectionIgnored(header string) {
	reason := sanitizeFlagSelectionIgnoredReason(header)
	if !firstFlagSelectionReport(flagSelectionIgnoredMetric + ":" + reason) {
		return
	}
	containLoggerPanic(func() {
		switch reason {
		case "malformed_selection":
			log.Error("openfeature: ProviderConfig.FlagSelection was ignored by the server (malformed_selection). Loaded the full flag configuration.") //errtrack:ignore remote service response
		case "too_many_values":
			log.Error("openfeature: ProviderConfig.FlagSelection was ignored by the server (too_many_values). Loaded the full flag configuration.") //errtrack:ignore remote service response
		case "invalid_key_pattern":
			log.Error("openfeature: ProviderConfig.FlagSelection was ignored by the server (invalid_key_pattern). Loaded the full flag configuration.") //errtrack:ignore remote service response
		case "invalid_tag":
			log.Error("openfeature: ProviderConfig.FlagSelection was ignored by the server (invalid_tag). Loaded the full flag configuration.") //errtrack:ignore remote service response
		case "tag_load_failed":
			log.Error("openfeature: ProviderConfig.FlagSelection was ignored by the server (tag_load_failed). Loaded the full flag configuration.") //errtrack:ignore remote service response
		default:
			log.Error("openfeature: ProviderConfig.FlagSelection was ignored by the server (unknown). Loaded the full flag configuration.") //errtrack:ignore remote service response
		}
	})
	countFlagSelection(flagSelectionIgnoredMetric, reason)
}

// warnFlagSelectionWithRemoteConfig warns once per process that Remote Config
// delivers every flag, when s selects anything or fails validation.
func warnFlagSelectionWithRemoteConfig(s *FlagSelection) {
	if selection, drop := canonicalizeFlagSelection(s); drop == flagSelectionNotDropped && selection.empty() {
		return
	}
	if !firstFlagSelectionReport("remote_config") {
		return
	}
	containLoggerPanic(func() {
		log.Warn("openfeature: ProviderConfig.FlagSelection is not supported when flags are delivered through Remote Config. Loading the full flag configuration.") //errtrack:ignore unsupported user configuration
	})
}
