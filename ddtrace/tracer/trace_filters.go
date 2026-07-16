// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"maps"
	"regexp"
	"strconv"
	"strings"

	normalize "github.com/DataDog/datadog-agent/pkg/trace/traceutil/normalize"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

type tagKV struct {
	key string
	val string
}

type regexTag struct {
	key *regexp.Regexp
	val *regexp.Regexp
}

type traceFilters struct {
	ignoreResources           []*regexp.Regexp
	rejectKeys, requireKeys   []string
	rejectKV, requireKV       []tagKV
	rejectRegex, requireRegex []regexTag
}

func newTraceFilters(exactRequire, exactReject, regexRequire, regexReject, ignoreResources []string) *traceFilters {
	f := new(traceFilters)
	f.requireKeys, f.requireKV = parseExactTagFilters(exactRequire)
	f.rejectKeys, f.rejectKV = parseExactTagFilters(exactReject)
	f.requireRegex = parseRegexTagFilters(regexRequire)
	f.rejectRegex = parseRegexTagFilters(regexReject)
	f.ignoreResources = compileRegexes(ignoreResources)
	if !f.hasFilters() {
		return nil
	}
	return f
}

func parseExactTagFilters(filters []string) (keys []string, kvs []tagKV) {
	for _, filter := range filters {
		if filter == "" {
			continue
		}
		key, val, hasValue := strings.Cut(filter, ":")
		key = strings.TrimSpace(key)
		if hasValue {
			kvs = append(kvs, tagKV{key: key, val: strings.TrimSpace(val)})
		} else {
			keys = append(keys, key)
		}
	}
	return keys, kvs
}

func parseRegexTagFilters(filters []string) []regexTag {
	parsed := make([]regexTag, 0, len(filters))
	for _, filter := range filters {
		if filter == "" {
			continue
		}
		keyPattern, valPattern, hasValue := strings.Cut(filter, ":")
		key, err := regexp.Compile(strings.TrimSpace(keyPattern))
		if err != nil {
			log.Debug("Skipping invalid agent trace filter regex %q: %v", filter, err.Error())
			continue
		}
		var val *regexp.Regexp
		if hasValue {
			val, err = regexp.Compile(strings.TrimSpace(valPattern))
			if err != nil {
				log.Debug("Skipping invalid agent trace filter regex %q: %v", filter, err.Error())
				continue
			}
		}
		parsed = append(parsed, regexTag{key: key, val: val})
	}
	return parsed
}

func compileRegexes(filters []string) []*regexp.Regexp {
	compiled := make([]*regexp.Regexp, 0, len(filters))
	for _, filter := range filters {
		if filter == "" {
			continue
		}
		re, err := regexp.Compile(filter)
		if err != nil {
			log.Debug("Skipping invalid agent trace filter regex %q: %v", filter, err.Error())
			continue
		}
		compiled = append(compiled, re)
	}
	return compiled
}

func (f *traceFilters) hasFilters() bool {
	return f != nil && (len(f.ignoreResources) > 0 || len(f.rejectKeys) > 0 || len(f.requireKeys) > 0 ||
		len(f.rejectKV) > 0 || len(f.requireKV) > 0 || len(f.rejectRegex) > 0 || len(f.requireRegex) > 0)
}

// +checklocks:root.mu
func (f *traceFilters) reject(root *Span) bool {
	resource := root.resource
	if resource == "" {
		resource, _ = normalize.NormalizeName(root.name)
	}
	for _, re := range f.ignoreResources {
		if re.MatchString(resource) {
			return true
		}
	}

	tags := maps.Collect(root.meta.All())
	if env, ok := tags[ext.Environment]; ok {
		tags[ext.Environment] = normalize.NormalizeTagValue(env)
	}
	if status, ok := tags[ext.HTTPCode]; ok && !validStatusCode(status) {
		delete(tags, ext.HTTPCode)
	}

	for _, key := range f.rejectKeys {
		if _, ok := tags[key]; ok {
			return true
		}
	}
	for _, filter := range f.rejectKV {
		if val, ok := tags[filter.key]; ok && val == filter.val {
			return true
		}
	}
	for _, filter := range f.rejectRegex {
		if matchRegexTag(tags, filter) {
			return true
		}
	}
	for _, key := range f.requireKeys {
		if _, ok := tags[key]; !ok {
			return true
		}
	}
	for _, filter := range f.requireKV {
		if val, ok := tags[filter.key]; !ok || val != filter.val {
			return true
		}
	}
	for _, filter := range f.requireRegex {
		if !matchRegexTag(tags, filter) {
			return true
		}
	}
	return false
}

func matchRegexTag(tags map[string]string, filter regexTag) bool {
	for key, val := range tags {
		if filter.key.MatchString(key) && (filter.val == nil || filter.val.MatchString(val)) {
			return true
		}
	}
	return false
}

func validStatusCode(status string) bool {
	code, err := strconv.Atoi(status)
	return err == nil && code >= 100 && code < 600
}
