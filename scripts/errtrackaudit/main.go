// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Command errtrackaudit reports the internal/log Error/Warn call sites in the
// root module that may want to adopt the Error Tracking reporting API
// (internal/telemetry/log.ReportError and friends), grouped by the CODEOWNERS
// team that owns each file. It is the triage tool for the adoption policy in
// internal/README.md ("When to report, and when not to"); its
// CANDIDATE / LIKELY_INELIGIBLE labels are textual heuristics, never an
// eligibility verdict.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	var (
		root    = flag.String("root", ".", "repository root")
		format  = flag.String("format", "table", "output format: table or json")
		pkgPref = flag.String("package", "", "restrict output to call sites whose package path (relative to the module root) starts with this prefix")
	)
	flag.Parse()

	if err := run(runOptions{root: *root, format: *format, pkgPrefix: *pkgPref, out: os.Stdout}); err != nil {
		fmt.Fprintln(os.Stderr, "errtrackaudit:", err)
		os.Exit(1)
	}
}

// runOptions parameterizes run with named fields instead of three strings
// of different meaning.
type runOptions struct {
	root      string // repository root, as given
	format    string // output format: table or json
	pkgPrefix string // restricts output to sites whose package path starts with this prefix
	out       io.Writer
}

func run(opts runOptions) error {
	switch opts.format {
	case "table", "json":
	default:
		return fmt.Errorf("unknown format %q", opts.format)
	}
	absRoot, err := filepath.Abs(opts.root)
	if err != nil {
		return err
	}
	owners, err := loadCodeowners(filepath.Join(absRoot, codeownersFile))
	if err != nil {
		return err
	}
	sites, err := scan(absRoot, defaultScanOptions())
	if err != nil {
		return err
	}
	sites = filterByPackage(sites, opts.pkgPrefix)
	rep := buildReport(sites, owners)
	switch opts.format {
	case "json":
		return renderJSON(opts.out, rep)
	case "table":
		return renderTable(opts.out, rep)
	default:
		return fmt.Errorf("unknown format %q", opts.format)
	}
}
