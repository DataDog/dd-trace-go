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
	"text/tabwriter"
)

// ConfigEntry is a DD_* env var grouped with the places it is read.
type ConfigEntry struct {
	Name      string     `json:"name"`
	CallSites []CallSite `json:"call_sites,omitempty"`
}

// AuditResult is the structured output of one audit run.
type AuditResult struct {
	Migrated                    []ConfigEntry `json:"migrated"`
	Unmigrated                  []ConfigEntry `json:"unmigrated"`
	Untracked                   []ConfigEntry `json:"untracked"`
	MigratedButStillReadOutside []ConfigEntry `json:"migrated_but_still_read_outside"`
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

func renderTable(w io.Writer, res AuditResult) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "STATUS\tCONFIG\tCALL_SITES\n")
	for _, e := range res.Unmigrated {
		fmt.Fprintf(tw, "UNMIGRATED\t%s\t%d\n", e.Name, len(e.CallSites))
	}
	for _, e := range res.MigratedButStillReadOutside {
		fmt.Fprintf(tw, "STILL_READ\t%s\t%d\n", e.Name, len(e.CallSites))
	}
	for _, e := range res.Untracked {
		fmt.Fprintf(tw, "UNTRACKED\t%s\t%d\n", e.Name, len(e.CallSites))
	}
	fmt.Fprintf(tw, "---\n")
	fmt.Fprintf(tw, "SUMMARY\tmigrated=%d\tunmigrated=%d\tuntracked=%d\tstill_read=%d\n",
		len(res.Migrated), len(res.Unmigrated), len(res.Untracked), len(res.MigratedButStillReadOutside))
	return tw.Flush()
}
