// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The two lanes that run internal/orchestrion/_integration suites.
const (
	laneOtelc       = "otelc"
	laneOrchestrion = "orchestrion"
)

// integrationRoot is where the orchestrion integration suites live, relative
// to the repository root.
const integrationRoot = "internal/orchestrion/_integration"

// suiteImportRef matches a quoted import path naming a suite package of the
// integration module, e.g.
// "github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/twirp".
// The capture group is the suite directory name; deeper packages (such as a
// suite's mainapp) do not match, so only whole-suite imports form edges.
var suiteImportRef = regexp.MustCompile(`"[^"]*/internal/orchestrion/_integration/([A-Za-z0-9._-]+)"`)

// SuiteVerdict is one lane's selection of integration suites. At most one of
// All and a non-empty Suites holds; both zero is the third verdict (NONE),
// and both consumers read it.
type SuiteVerdict struct {
	All    bool     `json:"all"`
	Suites []string `json:"suites"`
}

// loadSuiteDirs returns the suite names: the direct children of
// internal/orchestrion/_integration that contain Go files. The internal/
// helper tree is shared by every suite and is not a suite itself.
func loadSuiteDirs(root string) (map[string]bool, error) {
	entries, err := os.ReadDir(filepath.Join(root, integrationRoot))
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "internal" {
			continue
		}
		hasGo, err := dirHasGoFiles(filepath.Join(root, integrationRoot, e.Name()))
		if err != nil {
			return nil, err
		}
		if hasGo {
			out[e.Name()] = true
		}
	}
	return out, nil
}

func dirHasGoFiles(dir string) (bool, error) {
	found := false
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".go") {
			found = true
		}
		return nil
	})
	return found, err
}

// loadSuiteImporters scans each suite's Go files for imports of other suites
// and inverts the edges: importers[suite] lists the suites that import it.
// Suites share helper code this way (twirp_quantize imports twirp,
// parallel_testing_retries imports civisibilitytest), so a change to an
// imported suite must also run its importers.
func loadSuiteImporters(root string, suiteDirs map[string]bool) (map[string][]string, error) {
	importers := map[string][]string{}
	for suite := range suiteDirs {
		dir := filepath.Join(root, integrationRoot, suite)
		walkErr := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for _, m := range suiteImportRef.FindAllStringSubmatch(string(data), -1) {
				if name := m[1]; name != suite && suiteDirs[name] {
					importers[name] = append(importers[name], suite)
				}
			}
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	return importers, nil
}

// expandImporters returns set plus every suite that transitively imports a
// suite in it. allowed filters additions; nil admits every suite. The otelc
// lane passes its suite list so importers outside it stay out.
func expandImporters(set map[string]bool, importers map[string][]string, allowed func(string) bool) map[string]bool {
	out := make(map[string]bool, len(set))
	queue := make([]string, 0, len(set))
	for s := range set {
		out[s] = true
		queue = append(queue, s)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dep := range importers[cur] {
			if out[dep] || (allowed != nil && !allowed(dep)) {
				continue
			}
			out[dep] = true
			queue = append(queue, dep)
		}
	}
	return out
}

// selectSuites narrows the orchestrion and otelc lanes to the
// internal/orchestrion/_integration suites affected by the change set, then
// propagates each lane's gate from its verdict.
//
// Selection rules, per changed path: a path inside a suite directory names
// that suite; a path inside a contrib module names the suites its mapping
// lists, expanded through the module require graph (a change to a required
// module affects its dependents' suites); a file of the shared _integration
// module itself, or of a shared-modules entry, drives every suite. A path
// that turns a lane's gate on but names no suite for it fails open: that lane
// runs every suite, never zero. A path whose component gates neither lane
// contributes nothing at all -- a doc edit inside a code directory must not
// drag in that directory's suites. One refinement cuts against the fail-open:
// a suite directory whose import closure stays outside the otelc suite set
// proves the otelc lane unaffected -- that lane runs a fixed suite set and a
// suite reaches it only through the import graph -- so such a path suppresses
// the coarse component gate instead of failing the lane open.
func (t *table) selectSuites(root string, res *result) error {
	res.Suites = map[string]SuiteVerdict{
		laneOtelc:       {},
		laneOrchestrion: {},
	}
	if res.RunAll {
		res.Suites[laneOtelc] = SuiteVerdict{All: true}
		res.Suites[laneOrchestrion] = SuiteVerdict{All: true}
		return nil
	}

	otelcSuite := make(map[string]bool, len(t.IntegrationSuites.OtelcSuites))
	for _, s := range t.IntegrationSuites.OtelcSuites {
		otelcSuite[s] = true
	}
	sharedModule := make(map[string]bool, len(t.IntegrationSuites.SharedModules))
	for _, m := range t.IntegrationSuites.SharedModules {
		sharedModule[m] = true
	}
	suiteDirs, err := loadSuiteDirs(root)
	if err != nil {
		return err
	}
	importers, err := loadSuiteImporters(root, suiteDirs)
	if err != nil {
		return err
	}
	byPath, byDir, err := loadModules(root)
	if err != nil {
		return err
	}

	var (
		sel    = map[string]map[string]bool{laneOtelc: {}, laneOrchestrion: {}}
		esc    = map[string]bool{laneOtelc: false, laneOrchestrion: false}
		shared bool
		// Module contributions expand through the require graph. A seed whose
		// own mapping is empty fails open regardless of the closure.
		seeds     = map[string]bool{}
		seedFiles = map[string]string{}
		// Suite directories whose closure stays outside the otelc suite set;
		// they refine the coarse orchestrion-suite gate away for the otelc
		// lane instead of failing it open.
		suppressedOtelc = map[string]bool{}
	)

	paths := pathsOf(res)
	for _, file := range paths {
		// A path whose component gates neither lane contributes nothing: the
		// docs component claims README.md everywhere, including inside code
		// directories, and static-docs says nothing about suites.
		if !res.pathGatesHave(file, laneOtelc) && !res.pathGatesHave(file, laneOrchestrion) {
			continue
		}
		if rest, ok := strings.CutPrefix(file, integrationRoot+"/"); ok {
			if name, _, _ := strings.Cut(rest, "/"); suiteDirs[name] {
				// The orchestrion lane always learns from a suite directory.
				sel[laneOrchestrion][name] = true
				// The otelc lane runs a fixed suite set, and a suite reaches it
				// only through the import graph, so the otelc-filtered closure
				// is trusted narrowing even when it comes out empty: the
				// coarse component gate is then suppressed for this path
				// instead of failing the lane open.
				otelcClosure := expandImporters(map[string]bool{name: true},
					importers, func(s string) bool { return otelcSuite[s] })
				if !otelcSuite[name] {
					delete(otelcClosure, name)
				}
				for s := range otelcClosure {
					sel[laneOtelc][s] = true
				}
				if len(otelcClosure) == 0 {
					suppressedOtelc[file] = true
				}
				continue
			}
			// A file of the _integration module itself: its go.mod, its tool
			// file, testdata, the internal helper tree. All suites compile
			// against it, so every suite must run.
			shared = true
			continue
		}
		if m, ok := enclosingModule(byDir, file); ok {
			if sharedModule[m.Dir] {
				// Cross-cutting modules reach suites that never import them
				// (the weavers inject net/http spans into 20+ suites), so a
				// change runs the full lane instead of a narrowed set.
				shared = true
				continue
			}
			seeds[m.Dir] = true
			seedFiles[file] = m.Dir
			continue
		}
		// Root-module code and non-contrib trees contribute no suite; their
		// lanes narrow through fail-open only.
		t.failOpen(res, file, nil, otelcSuite, esc)
	}

	if len(seeds) > 0 {
		rev, err := reverseDeps(root, byPath)
		if err != nil {
			return err
		}
		closure := requireClosure(seeds, rev)
		// moduleSuites holds, per seed module, the orchestrion-lane suites
		// its mapping lists; the otelc lane derives from the same set.
		moduleSuites := map[string]map[string]bool{}
		for dir := range closure {
			suites, ok := t.IntegrationSuites.Modules[dir]
			if !ok {
				continue
			}
			set := make(map[string]bool, len(suites))
			for _, s := range suites {
				set[s] = true
				sel[laneOrchestrion][s] = true
				if otelcSuite[s] {
					sel[laneOtelc][s] = true
				}
			}
			moduleSuites[dir] = set
		}
		for file, dir := range seedFiles {
			t.failOpen(res, file, moduleSuites[dir], otelcSuite, esc)
		}
	}

	// Suites import other suites; follow the edges so a change to an
	// imported helper suite also runs its importers.
	sel[laneOrchestrion] = expandImporters(sel[laneOrchestrion], importers, nil)
	sel[laneOtelc] = expandImporters(sel[laneOtelc], importers, func(s string) bool { return otelcSuite[s] })

	for _, lane := range []string{laneOtelc, laneOrchestrion} {
		switch {
		case shared || esc[lane]:
			res.Suites[lane] = SuiteVerdict{All: true}
		case len(sel[lane]) > 0:
			out := make([]string, 0, len(sel[lane]))
			for s := range sel[lane] {
				out = append(out, s)
			}
			sort.Strings(out)
			res.Suites[lane] = SuiteVerdict{Suites: out}
		}
		// Gate propagation: a lane with a real selection runs, even when the
		// claiming component did not gate the lane -- a contrib/gin-gonic/gin
		// change must still turn the otelc lane on for the gin suite.
		if res.Suites[lane].All || len(res.Suites[lane].Suites) > 0 {
			res.Gates[lane] = true
			continue
		}
		// A NONE verdict keeps the component gate only for paths that were
		// not suppressed. Every un-suppressed gate-on path without narrowing
		// information would have failed the lane open above, so only the
		// suppressed suite directories remain to refine away.
		gate := false
		for _, file := range paths {
			if res.pathGatesHave(file, lane) && !(lane == laneOtelc && suppressedOtelc[file]) {
				gate = true
				break
			}
		}
		res.Gates[lane] = gate
	}
	return nil
}

// pathsOf returns the classified changed paths in a stable order.
func pathsOf(res *result) []string {
	paths := make([]string, 0, len(res.Explain))
	for p := range res.Explain {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// failOpen escalates each lane the path's component gates when the path
// learned nothing for that lane: suites is the per-lane contribution after
// the otelc intersection (nil means none). A gate-on lane without narrowing
// information must run the full suite, never zero suites.
func (t *table) failOpen(res *result, file string, suites map[string]bool, otelcSuite map[string]bool, esc map[string]bool) {
	for _, lane := range []string{laneOtelc, laneOrchestrion} {
		learned := len(suites)
		if lane == laneOtelc {
			learned = 0
			for s := range suites {
				if otelcSuite[s] {
					learned = 1
					break
				}
			}
		}
		if learned == 0 && res.pathGatesHave(file, lane) {
			esc[lane] = true
		}
	}
}

// requireClosure returns seeds plus every module that transitively requires
// one of them, mirroring selectContribModules' matrix expansion.
func requireClosure(seeds map[string]bool, rev map[string][]string) map[string]bool {
	closure := make(map[string]bool, len(seeds))
	queue := make([]string, 0, len(seeds))
	for m := range seeds {
		closure[m] = true
		queue = append(queue, m)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dependent := range rev[cur] {
			if !closure[dependent] {
				closure[dependent] = true
				queue = append(queue, dependent)
			}
		}
	}
	return closure
}

// suiteSelection renders a verdict for -explain.
func suiteSelection(v SuiteVerdict) string {
	switch {
	case v.All:
		return selectAll
	case len(v.Suites) == 0:
		return selectNone
	default:
		return strings.Join(v.Suites, ", ")
	}
}

// writeSuites emits a verdict for the -otelc-suites and -orchestrion-suites
// flags: one suite name per line, or the ALL/NONE sentinels the consumers
// already know from -contrib-modules.
func writeSuites(w io.Writer, v SuiteVerdict) {
	if v.All {
		fmt.Fprintln(w, selectAll)
		return
	}
	if len(v.Suites) == 0 {
		fmt.Fprintln(w, selectNone)
		return
	}
	for _, s := range v.Suites {
		fmt.Fprintln(w, s)
	}
}
