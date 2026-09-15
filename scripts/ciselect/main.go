// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Command ciselect decides which CI work a change set requires.
//
// It reads .github/ci-components.yml, matches each changed path against the
// component table, and reports the union of the gates those components need.
// A path that matches nothing enables every gate: the table is an allowlist of
// provably-skippable paths, so an unrecognised path is always treated as
// potentially affecting everything.
//
// Usage:
//
//	ciselect -changed-files <file> -github-output   # KEY=true|false lines
//	ciselect -changed-files <file> -json            # full verdict
//	ciselect -changed-files <file> -explain         # human-readable, for job summaries
//	ciselect -changed-files <file> -contrib-modules # module dirs, or ALL
//
// With no -changed-files, the list is read from stdin. An empty list means
// "unknown", which enables every gate.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ciselect:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("ciselect", flag.ContinueOnError)
	changedFiles := fs.String("changed-files", "", "file holding one changed path per line; defaults to stdin")
	asGitHub := fs.Bool("github-output", false, "emit GITHUB_OUTPUT key=value lines")
	asJSON := fs.Bool("json", false, "emit the full verdict as JSON")
	asExplain := fs.Bool("explain", false, "emit a human-readable summary")
	asContrib := fs.Bool("contrib-modules", false, "emit contrib module directories to test, or ALL")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := repoRoot(".")
	if err != nil {
		return err
	}
	t, err := loadTable(root)
	if err != nil {
		return err
	}
	graph, err := loadWorkflowGraph(root)
	if err != nil {
		return err
	}

	src := stdin
	if *changedFiles != "" {
		f, err := os.Open(*changedFiles)
		if err != nil {
			return err
		}
		defer f.Close()
		src = f
	}
	changed, err := readPaths(src)
	if err != nil {
		return err
	}

	res, err := t.classify(changed, graph)
	if err != nil {
		return err
	}

	switch {
	case *asContrib:
		mods, err := selectContribModules(root, res)
		if err != nil {
			return err
		}
		if len(mods) == 0 {
			mods = []string{selectNone}
		}
		for _, m := range mods {
			fmt.Fprintln(stdout, m)
		}
	case *asJSON:
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	case *asExplain:
		writeExplain(stdout, t, res)
	case *asGitHub:
		for _, gate := range t.allGates() {
			fmt.Fprintf(stdout, "%s=%t\n", gate, res.Gates[gate])
		}
		fmt.Fprintf(stdout, "run-all=%t\n", res.RunAll)
		// Convenience for the static-checks tool cache, which is needed if any
		// single static job runs.
		fmt.Fprintf(stdout, "static-any=%t\n", res.anyGateWithPrefix("static-"))
	default:
		for _, gate := range t.allGates() {
			if res.Gates[gate] {
				fmt.Fprintln(stdout, gate)
			}
		}
	}
	return nil
}

func readPaths(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}

func writeExplain(w io.Writer, t *table, res *result) {
	if res.RunAll {
		fmt.Fprintln(w, "Running the full suite.")
		for _, r := range res.Reasons {
			fmt.Fprintf(w, "  - %s\n", r)
		}
	} else {
		var on, off []string
		for _, g := range t.allGates() {
			if res.Gates[g] {
				on = append(on, g)
			} else {
				off = append(off, g)
			}
		}
		fmt.Fprintf(w, "Running: %s\n", strings.Join(on, ", "))
		fmt.Fprintf(w, "Skipping: %s\n", strings.Join(off, ", "))
	}

	paths := make([]string, 0, len(res.Explain))
	for p := range res.Explain {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	fmt.Fprintln(w, "\nChanged paths:")
	for _, p := range paths {
		fmt.Fprintf(w, "  %-60s %s\n", p, res.Explain[p])
	}
}
