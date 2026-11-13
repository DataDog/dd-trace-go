// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// tsLayout parses both GitHub API timestamps (2026-09-13T11:36:55Z) and
// runner-log timestamps, which carry nanosecond fractions
// (2026-09-13T11:37:05.8370630Z). Fractional digits are optional in this
// layout, so one layout covers both forms.
const tsLayout = "2006-01-02T15:04:05.999999999Z"

// check-name patterns the all-green workflow ignores; mirrored here so PR
// feedback excludes human-review and merge-queue waiting exactly like CI does.
var ignoredCheckPatterns = []string{
	"devflow/merge",
	"devflow/mergegate",
	"label_issues",
	"check-title",
	"dd-gitlab/",
	"DDCI Status",
}

var (
	logTSRe         = regexp.MustCompile(`^\x{FEFF}?(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z)`)
	cacheObsRe      = regexp.MustCompile(`cache-observation:(\{.*\})$`)
	saveExactSkipRe = regexp.MustCompile(`Cache hit occurred on the primary key .*not saving cache`)
	saveConflictRe  = regexp.MustCompile(`Failed to save: Unable to reserve cache with key .*another job may be creating`)
	saveErrorRe     = regexp.MustCompile(`Failed to save`)
	// Verified against real runner logs: actions/cache v6 writes
	// "Cache saved with key: ..." and actions/setup-go's post step writes
	// "Cache saved with the key: ...".
	saveSavedRe = regexp.MustCompile(`Cache saved with (?:the )?key:`)

	postMarker = "Post job cleanup."
)

// cacheObservation is the structured record .github/actions/setup-go prints
// into every job log.
type cacheObservation struct {
	Provider string `json:"provider"`
	Workload string `json:"workload"`
	Runtime  struct {
		Name      string `json:"name"`
		Requested string `json:"requested"`
		Version   string `json:"version"`
	} `json:"runtime"`
	Restores []restoreObs `json:"restores"`
}

// restoreObs is one restore's raw provider outputs. cache_matched_key is a
// pointer so its presence is distinguishable from an empty string. Restored
// is set by the cloudx setup step ("true" when the module cache is non-empty
// after the restore) and is empty when the record does not carry it.
// restore_source (exact / default / branch / none) and dep_match come from
// the setup-go wrapper's derive step and are empty for records emitted
// before that step existed or by other providers.
type restoreObs struct {
	Name            string  `json:"name"`
	Enabled         string  `json:"enabled"`
	Outcome         string  `json:"outcome"`
	CacheHit        string  `json:"cache_hit"`
	CacheMatchedKey *string `json:"cache_matched_key"`
	RestoreSource   string  `json:"restore_source"`
	DepMatch        string  `json:"dep_match"`
	Restored        string  `json:"restored"`
}

// restoreClassification is one restore as stored in the observation
// record. restore_source and dep_match are carried through from the raw
// observation when present. disagreement marks a cloudx setup_go entry
// whose matched key and restored cross-check contradict each other: a
// matched key with restored "false", or an empty matched key with
// restored "true". Such a record is evidence of a provider or wrapper
// defect, never a normal outcome.
type restoreClassification struct {
	Name          string `json:"name"`
	Result        string `json:"result"`
	RestoreSource string `json:"restore_source,omitempty"`
	DepMatch      string `json:"dep_match,omitempty"`
	Disagreement  bool   `json:"disagreement,omitempty"`
}

// classifyRestore classifies one restore observation from provider outputs.
// Ambiguous evidence is "unknown", never a guessed hit or miss. For the
// github-cache provider cache_hit is an exact-hit boolean and a non-empty
// cache_matched_key alongside cache_hit != true marks a prefix restore.
// For the cloudx provider a `false` cache_hit is ambiguous between a
// prefix restore and a cold miss; the record's restored field resolves it
// (prefix or cold_miss) and, when absent, it stays unknown. Only a `true`
// (an exact hit) is unambiguous on its own.
func classifyRestore(obs *restoreObs, provider string) string {
	if obs == nil {
		return "unknown"
	}
	if !strings.EqualFold(obs.Enabled, "true") {
		return "disabled"
	}
	switch obs.Outcome {
	case "":
		return "unknown"
	case "success":
	default:
		return "error"
	}
	hit := strings.ToLower(obs.CacheHit)
	// CloudX semantics apply only to the isolated build/module cache entry,
	// which never carries a matched key; the tools entry keeps its
	// actions/cache semantics even inside a merged cloudx observation.
	if provider == "cloudx" && obs.CacheMatchedKey == nil {
		switch {
		case hit == "true":
			return "exact"
		case strings.EqualFold(obs.Restored, "true"):
			return "prefix"
		case strings.EqualFold(obs.Restored, "false"):
			return "cold_miss"
		default:
			return "unknown"
		}
	}
	if obs.CacheMatchedKey != nil {
		switch {
		case hit == "true":
			return "exact"
		case hit == "false" || hit == "":
			if *obs.CacheMatchedKey != "" {
				return "prefix"
			}
			return "cold_miss"
		default:
			return "unknown"
		}
	}
	switch hit {
	case "true":
		return "exact"
	case "false":
		return "cold_miss"
	default:
		return "unknown"
	}
}

// restoreDisagrees reports whether the matched key and the restored
// cross-check contradict each other on a cloudx setup_go entry: a matched
// key with restored "false" (an entry matched but the module cache stayed
// empty), or an empty matched key with restored "true" (no entry matched
// but the module cache was non-empty). Only a cloudx setup_go entry can
// disagree: it is the only entry whose matched key and restored flag
// describe the same restore.
func restoreDisagrees(obs *restoreObs, provider string) bool {
	if provider != "cloudx" || obs == nil || obs.Name != "setup_go" || obs.CacheMatchedKey == nil {
		return false
	}
	switch {
	case *obs.CacheMatchedKey != "" && strings.EqualFold(obs.Restored, "false"):
		return true
	case *obs.CacheMatchedKey == "" && strings.EqualFold(obs.Restored, "true"):
		return true
	}
	return false
}

// classifySaves classifies every cache save event in the log, in order. One
// job typically saves several caches (repo, tools, toolchain, build caches),
// so a single job-level label would misreport mixed outcomes.
func classifySaves(body string) []string {
	saves := make([]string, 0, 4)
	for line := range strings.SplitSeq(body, "\n") {
		switch {
		case saveSavedRe.MatchString(line):
			saves = append(saves, "saved")
		case saveConflictRe.MatchString(line):
			saves = append(saves, "conflict")
		case saveExactSkipRe.MatchString(line):
			saves = append(saves, "exact_key_skip")
		case saveErrorRe.MatchString(line):
			saves = append(saves, "error")
		}
	}
	return saves
}

// logEvidence is everything extracted from one job's log.
type logEvidence struct {
	observation *cacheObservation
	postSeconds *float64
	saves       []string
}

// parseLog extracts cache evidence from one job's log.
func parseLog(text string) *logEvidence {
	if text == "" {
		return nil
	}
	var postStart, last time.Time
	havePost := false
	var observation *cacheObservation
	for line := range strings.SplitSeq(text, "\n") {
		m := logTSRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		when, err := time.Parse(tsLayout, m[1])
		if err != nil {
			continue
		}
		rest := line[len(m[0]):]
		if !havePost && strings.Contains(rest, postMarker) {
			postStart = when
			havePost = true
		}
		last = when
		// A job may print more than one cache-observation record; a
		// later merged record (for example one that folds the tools
		// restore into the build-cache observation) supersedes an
		// earlier partial one. Keep the LAST observation in the log.
		if om := cacheObsRe.FindStringSubmatch(rest); om != nil {
			var obs cacheObservation
			if json.Unmarshal([]byte(om[1]), &obs) == nil {
				observation = &obs
			}
		}
	}
	ev := &logEvidence{observation: observation}
	if havePost && !last.IsZero() {
		secs := last.Sub(postStart).Seconds()
		ev.postSeconds = &secs
	}
	ev.saves = classifySaves(text)
	return ev
}

func parseTS(value string) (time.Time, error) {
	return time.Parse(tsLayout, value)
}

func secondsBetween(start, end string) *float64 {
	if start == "" || end == "" {
		return nil
	}
	s, err := parseTS(start)
	if err != nil {
		return nil
	}
	e, err := parseTS(end)
	if err != nil {
		return nil
	}
	secs := e.Sub(s).Seconds()
	return &secs
}

func fmtSeconds(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return strconv.FormatFloat(*v, 'f', 1, 64)
}
