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
//
// By default the output is an array of strings, one space-separated module list
// per runner, which the smoke tests consume. With -shards the output is an array
// of objects, one per non-empty shard:
//
//	{"shard":"k/6","modules":"./contrib/a/ ./contrib/b/ ","count":2}
//
// A module belongs to the same shard whether or not -select narrows the list,
// so the shard name is a cache identity that pull requests share with the
// nightly runs. The job count is the number of non-empty shards, not the
// pre-change minimum of maxRunners and the selected module count.
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
	shards := flag.Bool("shards", false, "emit one {shard, modules, count} object per shard instead of strings")
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

	var allModules []string
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
		allModules = append(allModules, rel)
	}

	var output any
	if *shards {
		output = shardEntries(allModules, selected)
	} else {
		output = legacyChunks(allModules, selected)
	}

	jsonText, err := json.Marshal(output)
	if err != nil {
		log.Fatalln(err)
	}

	if _, err := fmt.Fprintln(os.Stdout, string(jsonText)); err != nil {
		log.Fatalln(err)
	}
}

// shardEntry is one runner of the -shards output.
type shardEntry struct {
	Shard   string `json:"shard"`
	Modules string `json:"modules"`
	Count   int    `json:"count"`
}

// shardAssignment splits the complete, ordered module list into maxRunners
// shards, round-robin. The shard index is the cache identity of a runner, so
// this is the one place that decides which shard owns a module.
//
// To stop an insertion in go.work from moving every later module to another
// shard, replace the body with the salted hash H(salt‖dir) mod maxRunners,
// where the salt is pinned once so that the shards hold 15 or 16 modules.
// Callers need no other change. Until the nightly run re-seeds, an
// insertion leaves pull requests restoring a shard seeded with different
// modules.
func shardAssignment(modules []string) [][]string {
	shards := make([][]string, maxRunners)
	for i, rel := range modules {
		shards[i%maxRunners] = append(shards[i%maxRunners], rel)
	}
	return shards
}

// shardEntries assigns every module to a shard first and filters by the
// selection second. A nil selection keeps every module. Shards left empty by
// the selection are omitted, because each one would boot a runner to test
// nothing.
func shardEntries(allModules []string, selected map[string]bool) []shardEntry {
	entries := []shardEntry{}
	for k, shard := range shardAssignment(allModules) {
		var kept []string
		for _, rel := range shard {
			if isSelected(rel, selected) {
				kept = append(kept, rel)
			}
		}
		if len(kept) == 0 {
			continue
		}
		entries = append(entries, shardEntry{
			Shard:   fmt.Sprintf("%d/%d", k, maxRunners),
			Modules: moduleList(kept),
			Count:   len(kept),
		})
	}
	return entries
}

// legacyChunks is the default output: the selected modules, dealt round-robin
// over at most maxRunners strings. The smoke tests consume this form, so its
// bytes must not change.
func legacyChunks(allModules []string, selected map[string]bool) []string {
	var modules []string
	for _, rel := range allModules {
		if isSelected(rel, selected) {
			modules = append(modules, rel)
		}
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
	return contribs
}

// isSelected reports whether rel is kept. An unselected module is dropped
// rather than reported: the selection is computed from the filesystem and can
// name modules that are absent from go.work, which `go list -m` never
// enumerates. Narrowing only ever removes work, so a mismatch here cannot add
// an untested module.
func isSelected(rel string, selected map[string]bool) bool {
	return selected == nil || selected[rel]
}

func moduleList(modules []string) string {
	var b strings.Builder
	for _, rel := range modules {
		b.WriteString("./" + rel + "/ ")
	}
	return b.String()
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
