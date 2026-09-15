// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
)

// SiteRow is one reported call site.
type SiteRow struct {
	File           string `json:"file"`
	Line           int    `json:"line"`
	Package        string `json:"package"`
	Func           string `json:"func,omitempty"`
	Level          string `json:"level"`
	Message        string `json:"message"`
	Classification string `json:"classification"`
}

// Totals counts the sites behind one group. Sites counts every scanned site,
// including ignored ones, so a group whose backlog is fully worked shows
// Sites == Ignored with an empty site list.
type Totals struct {
	Sites            int `json:"sites"`
	Candidate        int `json:"candidate"`
	LikelyIneligible int `json:"likely_ineligible"`
	Ignored          int `json:"ignored"`
}

// OwnerGroup is every scanned call site in files owned by one team.
type OwnerGroup struct {
	Owner  string    `json:"owner"`
	Totals Totals    `json:"totals"`
	Sites  []SiteRow `json:"sites"`
}

// Report is the structured output of one audit run.
type Report struct {
	Summary Totals       `json:"summary"`
	Owners  []OwnerGroup `json:"owners"`
}

// buildReport groups scanned sites by their CODEOWNERS team, classifies each
// non-ignored site's message, and computes the per-owner and overall totals.
// Output is deterministic: owners sort by name, sites by file and line.
func buildReport(sites []Site, co *codeowners) Report {
	groups := map[string]*OwnerGroup{}
	var rep Report
	for _, s := range sites {
		key := strings.Join(co.ownersFor(s.File), " ")
		g := groups[key]
		if g == nil {
			g = &OwnerGroup{Owner: key}
			groups[key] = g
		}
		g.Totals.Sites++
		rep.Summary.Sites++
		if s.Ignored {
			g.Totals.Ignored++
			rep.Summary.Ignored++
			continue
		}
		row := SiteRow{
			File:    s.File,
			Line:    s.Line,
			Package: s.Package,
			Func:    s.Func,
			Level:   s.Level,
			Message: s.Message,
		}
		if classifyMessage(s.Message) == classificationLikelyIneligible {
			row.Classification = classificationLikelyIneligible
			g.Totals.LikelyIneligible++
			rep.Summary.LikelyIneligible++
		} else {
			row.Classification = classificationCandidate
			g.Totals.Candidate++
			rep.Summary.Candidate++
		}
		g.Sites = append(g.Sites, row)
	}
	for _, g := range groups {
		slices.SortStableFunc(g.Sites, func(a, b SiteRow) int {
			if c := strings.Compare(a.File, b.File); c != 0 {
				return c
			}
			return a.Line - b.Line
		})
		rep.Owners = append(rep.Owners, *g)
	}
	slices.SortFunc(rep.Owners, func(a, b OwnerGroup) int {
		return strings.Compare(a.Owner, b.Owner)
	})
	return rep
}

// filterByPackage restricts sites to those whose package path (relative to
// the module root) has the given prefix. An empty prefix returns sites
// unchanged.
func filterByPackage(sites []Site, prefix string) []Site {
	if prefix == "" {
		return sites
	}
	// Build a new slice rather than filtering in place: the input is shared
	// with the caller and slices.DeleteFunc zeroes the discarded tail.
	var out []Site
	for _, s := range sites {
		if strings.HasPrefix(s.Package, prefix) {
			out = append(out, s)
		}
	}
	return out
}

func renderJSON(w io.Writer, rep Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

var tableCellEscaper = strings.NewReplacer("\r", `\r`, "\n", `\n`, "\t", `\t`)

func renderTable(w io.Writer, rep Report) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for i, g := range rep.Owners {
		if i > 0 {
			fmt.Fprintln(tw)
		}
		fmt.Fprintf(tw, "OWNER: %s — %d sites (%d CANDIDATE, %d LIKELY_INELIGIBLE, %d ignored)\n",
			g.Owner, g.Totals.Sites, g.Totals.Candidate, g.Totals.LikelyIneligible, g.Totals.Ignored)
		fmt.Fprintf(tw, "  FILE\tLINE\tLEVEL\tCLASSIFICATION\tMESSAGE\n")
		for _, s := range g.Sites {
			fmt.Fprintf(tw, "  %s\t%d\t%s\t%s\t%s\n", s.File, s.Line, s.Level, s.Classification, tableCellEscaper.Replace(s.Message))
		}
	}
	if len(rep.Owners) > 0 {
		fmt.Fprintln(tw)
	}
	fmt.Fprintf(tw, "SUMMARY: %d sites (%d CANDIDATE, %d LIKELY_INELIGIBLE, %d ignored) across %d owners\n",
		rep.Summary.Sites, rep.Summary.Candidate, rep.Summary.LikelyIneligible, rep.Summary.Ignored, len(rep.Owners))
	return tw.Flush()
}
