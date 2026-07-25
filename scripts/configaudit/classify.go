// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// ConfigEntry is a DD_* env var grouped with the places it is read.
type ConfigEntry struct {
	Name      string     `json:"name"`
	CallSites []CallSite `json:"call_sites,omitempty"`
}

// ScopeModule identifies a nested module excluded from the root-module audit.
type ScopeModule struct {
	Path string `json:"path"`
	Dir  string `json:"dir"`
}

// AuditScope describes the root module and every nested module excluded from
// the audit.
type AuditScope struct {
	RootModule      string        `json:"root_module"`
	ExcludedModules []ScopeModule `json:"excluded_modules"`
}

// AuditResult is the structured output of one audit run.
type AuditResult struct {
	Scope                       AuditScope    `json:"scope"`
	Migrated                    []ConfigEntry `json:"migrated"`
	Unmigrated                  []ConfigEntry `json:"unmigrated"`
	Untracked                   []ConfigEntry `json:"untracked"`
	MigratedButStillReadOutside []ConfigEntry `json:"migrated_but_still_read_outside"`
	Unresolved                  []Finding     `json:"unresolved"`
	Suppressions                []Finding     `json:"suppressions"`
	CoverageErrors              []string      `json:"coverage_errors"`
}

// Clean reports whether the audit proved that every configuration read is
// tracked and migrated for every supported build variant.
func (res AuditResult) Clean() bool {
	return len(res.Unmigrated) == 0 &&
		len(res.MigratedButStillReadOutside) == 0 &&
		len(res.Untracked) == 0 &&
		len(res.Unresolved) == 0 &&
		len(res.Suppressions) == 0 &&
		len(res.CoverageErrors) == 0
}

func classify(known, migrated map[string]struct{}, reads map[string][]CallSite) AuditResult {
	var res AuditResult
	// All known keys: emit either as migrated or as "known but not migrated and not read" (skipped).
	// We only emit migrated entries that actually have a corresponding migrated marker.
	for key := range migrated {
		res.Migrated = append(res.Migrated, ConfigEntry{Name: key})
		if sites, ok := reads[key]; ok {
			res.MigratedButStillReadOutside = append(res.MigratedButStillReadOutside, ConfigEntry{Name: key, CallSites: sites})
		}
	}
	for key, sites := range reads {
		if _, isMigrated := migrated[key]; isMigrated {
			continue
		}
		entry := ConfigEntry{Name: key, CallSites: sites}
		if _, isKnown := known[key]; isKnown {
			res.Unmigrated = append(res.Unmigrated, entry)
		} else {
			res.Untracked = append(res.Untracked, entry)
		}
	}
	sortEntries(res.Migrated)
	sortEntries(res.Unmigrated)
	sortEntries(res.Untracked)
	sortEntries(res.MigratedButStillReadOutside)
	return res
}

func sortEntries(es []ConfigEntry) {
	sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
}

func renderJSON(w io.Writer, res AuditResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

// pkgRow is one row in the per-package view: a status, env var name, and
// the number of call sites in this package.
type pkgRow struct {
	Status string
	Name   string
	Count  int
}

// groupByPackage walks the bucketed audit result and produces a
// package -> []pkgRow map. Buckets without call sites (Migrated) are not
// represented, since they have nothing to attribute to a package.
func groupByPackage(res AuditResult) map[string][]pkgRow {
	out := map[string][]pkgRow{}
	add := func(status string, entries []ConfigEntry) {
		for _, e := range entries {
			counts := map[string]int{}
			for _, cs := range e.CallSites {
				counts[cs.Package]++
			}
			for pkg, n := range counts {
				out[pkg] = append(out[pkg], pkgRow{Status: status, Name: e.Name, Count: n})
			}
		}
	}
	add("UNMIGRATED", res.Unmigrated)
	add("STILL_READ", res.MigratedButStillReadOutside)
	add("UNTRACKED", res.Untracked)
	return out
}

// shortPkg strips the dd-trace-go module prefix for readable rendering.
func shortPkg(path string) string {
	const prefix = "github.com/DataDog/dd-trace-go/v2/"
	return strings.TrimPrefix(path, prefix)
}

func renderTable(w io.Writer, res AuditResult) error {
	groups := groupByPackage(res)
	pkgs := make([]string, 0, len(groups))
	for p := range groups {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	wroteSection := false
	for i, pkg := range pkgs {
		if i > 0 {
			fmt.Fprintln(tw)
		}
		fmt.Fprintf(tw, "PACKAGE: %s\n", shortPkg(pkg))
		fmt.Fprintf(tw, "  STATUS\tCONFIG\tCALL_SITES\n")
		rows := groups[pkg]
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Status != rows[j].Status {
				return rows[i].Status < rows[j].Status
			}
			return rows[i].Name < rows[j].Name
		})
		for _, r := range rows {
			fmt.Fprintf(tw, "  %s\t%s\t%d\n", r.Status, r.Name, r.Count)
		}
		wroteSection = true
	}
	if len(res.Unresolved) > 0 || len(res.Suppressions) > 0 {
		if wroteSection {
			fmt.Fprintln(tw)
		}
		fmt.Fprintln(tw, "SYNTAX FINDINGS")
		fmt.Fprintln(tw, "  STATUS\tCONFIG\tLOCATION\tREADER")
		renderFindingRows(tw, "UNRESOLVED", res.Unresolved)
		renderFindingRows(tw, "SUPPRESSION", res.Suppressions)
		wroteSection = true
	}
	if len(res.CoverageErrors) > 0 {
		if wroteSection {
			fmt.Fprintln(tw)
		}
		fmt.Fprintln(tw, "COVERAGE ERRORS")
		errors := append([]string(nil), res.CoverageErrors...)
		sort.Strings(errors)
		for _, coverageErr := range errors {
			fmt.Fprintf(tw, "  COVERAGE_ERROR\t%s\n", coverageErr)
		}
	}
	return tw.Flush()
}

func renderFindingRows(w io.Writer, status string, findings []Finding) {
	rows := append([]Finding(nil), findings...)
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i].CallSite, rows[j].CallSite
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		return rows[i].Key < rows[j].Key
	})
	for _, finding := range rows {
		key := finding.Key
		if key == "" {
			key = "<dynamic>"
		}
		location := fmt.Sprintf("%s:%d", finding.CallSite.File, finding.CallSite.Line)
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", status, key, location, finding.CallSite.Func)
	}
}

// filterByPackage restricts the call-site map to call sites whose package
// import path has the given prefix (after stripping the dd-trace-go module
// prefix from both sides). An empty prefix returns reads unchanged.
func filterByPackage(reads map[string][]CallSite, prefix string) map[string][]CallSite {
	if prefix == "" {
		return reads
	}
	out := make(map[string][]CallSite)
	for key, sites := range reads {
		var kept []CallSite
		for _, cs := range sites {
			if strings.HasPrefix(shortPkg(cs.Package), prefix) {
				kept = append(kept, cs)
			}
		}
		if len(kept) > 0 {
			out[key] = kept
		}
	}
	return out
}
