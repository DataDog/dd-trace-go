// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build ignore

// This tool outputs a JSON encoded array that can be used as a matrix input to GitHub workflows.
// Rather than testing all contribs under one job, we would rather parallelize the jobs
// by using a matrix.
// The `APM Larger Runners` group shares around 50 runners. We should not use all 50.
// TODO: can we find an optimal number of runners that will make the test efficient without
// creating too much cost?
//
// With -select, only the listed module directories are kept. The file is
// produced by `go run ./scripts/ciselect -contrib-modules`, and the single
// token ALL (or no flag at all) means "test everything", which is what the
// nightly smoke tests rely on.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	maxRunners = 6
	selectAll  = "ALL"
	selectNone = "NONE"
)

func main() {
	selectFile := flag.String("select", "", "file listing module directories to test, or the token ALL")
	flag.Parse()

	selected, err := readSelection(*selectFile)
	if err != nil {
		log.Fatalln(err)
	}

	cmd := exec.Command("go", "list", "-m", "-json")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		log.Fatalln(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalln(err)
	}

	var modules []string
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var pkg struct {
			Path string `json:"Path"`
			Dir  string `json:"Dir"`
		}
		if err := dec.Decode(&pkg); err != nil {
			continue
		}

		rel, err := filepath.Rel(cwd, pkg.Dir)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		// we want to only count packages in the contrib or instrumentation directory
		if !strings.HasPrefix(rel, "contrib/") && !strings.HasPrefix(rel, "instrumentation/") {
			continue
		}
		// An unselected module is dropped rather than reported: the selection is
		// computed from the filesystem and can name modules that are absent from
		// go.work, which `go list -m` never enumerates. Narrowing only ever
		// removes work, so a mismatch here cannot add an untested module.
		if selected != nil && !selected[rel] {
			continue
		}
		modules = append(modules, rel)
	}

	runners := maxRunners
	if len(modules) < runners {
		// Otherwise the extra chunks are empty strings, and each one still costs
		// a runner that boots a 21-container docker-compose to test nothing.
		runners = len(modules)
	}

	contribs := make([]string, runners)
	for i, rel := range modules {
		contribs[i%runners] += "./" + rel + "/ "
	}

	jsonText, err := json.Marshal(contribs)
	if err != nil {
		log.Fatalln(err)
	}

	if _, err := fmt.Fprintln(os.Stdout, string(jsonText)); err != nil {
		log.Fatalln(err)
	}
}

// readSelection returns nil when every module should be tested.
func readSelection(path string) (map[string]bool, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Absent selection means we could not determine one. Test everything.
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	selected := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
		case line == selectAll:
			return nil, nil
		case line == selectNone:
			// Non-nil and empty: test no contrib module. Distinct from nil,
			// which means "no selection was made, so test everything".
		default:
			selected[line] = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return selected, nil
}
