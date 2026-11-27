// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The two lanes that run internal/orchestrion/_integration suites.
const (
	laneOtelc       = "otelc"
	laneOrchestrion = "orchestrion"
)

// integrationRoot is where the orchestrion integration suites live, relative
// to the repository root.
const integrationRoot = "internal/orchestrion/_integration"

const contribTree = "contrib"

// otelcExternalAppPrefixes are the inputs of the otelc lane's external-app
// job: it builds one application and runs no suites. Such a change
// contributes no suite and does not fail the otelc lane open -- a suite-less
// full-lane run would burn the three-OS matrix for a build -- but unlike a
// suppressed suite directory the component's gate vote stays, so the
// external-app job still has to build.
var otelcExternalAppPrefixes = []string{
	"internal/apps/otelc-external-app",
	"scripts/build_otelc_external_app.sh",
}

// feedsOtelcExternalApp reports whether file is an external-app job input.
func feedsOtelcExternalApp(file string) bool {
	for _, p := range otelcExternalAppPrefixes {
		if file == p || strings.HasPrefix(file, p+"/") {
			return true
		}
	}
	return false
}

// suiteImportRef matches a quoted import path naming a suite package of the
// integration module, e.g.
// "github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/twirp".
// The capture group is the suite directory name; deeper packages do not
// match, so only whole-suite imports form edges.
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

// loadOtelcSuitesOnly parses only the integration-suites section of
// .github/ci-components.yml, skipping full classifier validation so the
// fail-open ALL path in .github/workflows/otelc.yml still lists otelc suites
// when an unrelated gate or component rule is invalid. The integration-suites
// section itself is decoded with KnownFields(true) so a misspelled key such as
// otelc-extra-suite fails loudly instead of silently dropping suites.
func loadOtelcSuitesOnly(root string) (map[string]bool, error) {
	path := filepath.Join(root, tableRelPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rootDoc struct {
		IntegrationSuites yaml.Node `yaml:"integration-suites"`
	}
	if err := yaml.Unmarshal(raw, &rootDoc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if rootDoc.IntegrationSuites.Kind == 0 {
		return nil, fmt.Errorf("%s: missing integration-suites section", path)
	}
	rawSection, err := yaml.Marshal(&rootDoc.IntegrationSuites)
	if err != nil {
		return nil, fmt.Errorf("encode integration-suites in %s: %w", path, err)
	}
	t := &table{}
	dec := yaml.NewDecoder(bytes.NewReader(rawSection))
	dec.KnownFields(true)
	if err := dec.Decode(&t.IntegrationSuites); err != nil {
		return nil, fmt.Errorf("parse integration-suites in %s: %w", path, err)
	}
	if err := expandOtelcModuleSuites(root, t.IntegrationSuites.Modules); err != nil {
		return nil, fmt.Errorf("expand integration-suites in %s: %w", path, err)
	}
	return t.otelcSuites()
}

// otelcSuites returns the set of internal/orchestrion/_integration suite names
// that the otelc lane runs. The set is the union of:
//   - t.IntegrationSuites.OtelcExtraSuites (suites declared in
//     .github/ci-components.yml under integration-suites.otelc-extra-suites), and
//   - every suite listed in integration-suites.modules for a directory whose
//     path ends with "/otelc" (the otelc harness modules).
func (t *table) otelcSuites() (map[string]bool, error) {
	out := make(map[string]bool)
	for _, s := range t.IntegrationSuites.OtelcExtraSuites {
		out[s] = true
	}
	for dir, suites := range t.IntegrationSuites.Modules {
		if !strings.HasSuffix(dir, "/otelc") {
			continue
		}
		for _, s := range suites {
			out[s] = true
		}
	}
	if len(out) == 0 {
		return nil, errors.New(".github/ci-components.yml: otelc suite set is empty")
	}
	return out, nil
}

// discoverOtelcModules walks the repository for go.mod files inside a directory
// named "otelc" (skipping dot-prefixed directories and testdata). For each such
// directory it emits the otelc directory itself, its parent directory when the
// parent has its own go.mod (skipping root-module directories such as contrib/os
// that have no go.mod), and "otelc/all" (once). Returns a sorted, deduplicated
// list.
func discoverOtelcModules(root string) ([]string, error) {
	// Verify otelc/all has a go.mod.
	if _, err := os.Stat(filepath.Join(root, "otelc", "all", "go.mod")); err != nil {
		return nil, fmt.Errorf("otelc/all/go.mod: %w", err)
	}

	set := map[string]bool{"otelc/all": true}
	if err := walkOtelcModuleDirs(root, root, func(otelcDir string) error {
		set[otelcDir] = true
		parent, err := otelcParentModule(root, otelcDir)
		if parent != "" {
			set[parent] = true
		}
		return err
	}); err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(set)), nil
}

func discoverOtelcIntegrationPaths(root string) ([]string, error) {
	paths := map[string]bool{}
	if err := walkOtelcModuleDirs(root, filepath.Join(root, contribTree), func(otelcDir string) error {
		parent, err := otelcParentModule(root, otelcDir)
		if err != nil {
			return err
		}
		if parent != "" {
			paths[parent+"/**"] = true
		} else {
			paths[otelcDir+"/**"] = true
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(paths)), nil
}

func otelcParentModule(root, otelcDir string) (string, error) {
	parent := filepath.ToSlash(filepath.Dir(otelcDir))
	if parent == "." {
		return "", nil
	}
	info, err := os.Stat(filepath.Join(root, parent, "go.mod"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s/go.mod: %w", parent, err)
	}
	if info.IsDir() {
		return "", nil
	}
	return parent, nil
}

func expandOtelcModuleSuites(root string, modules map[string][]string) error {
	if len(modules) == 0 {
		return nil
	}
	var otelcDirs []string
	if err := walkOtelcModuleDirs(root, root, func(otelcDir string) error {
		otelcDirs = append(otelcDirs, otelcDir)
		return nil
	}); err != nil {
		return err
	}
	slices.Sort(otelcDirs)
	for _, otelcDir := range otelcDirs {
		if _, ok := modules[otelcDir]; ok {
			continue
		}
		parent := filepath.ToSlash(filepath.Dir(otelcDir))
		if suites, ok := modules[parent]; ok {
			modules[otelcDir] = slices.Clone(suites)
		}
	}
	return nil
}

func walkOtelcModuleDirs(root, dir string, visit func(string) error) error {
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		otelcDir := filepath.Dir(p)
		if d.Name() != "go.mod" || filepath.Base(otelcDir) != "otelc" {
			return nil
		}
		rel, err := filepath.Rel(root, otelcDir)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		return visit(filepath.ToSlash(rel))
	})
}

// selectSuites narrows the orchestrion and otelc lanes to the
// internal/orchestrion/_integration suites affected by the change set, then
// propagates each lane's gate from its verdict.
//
// Selection rules, per changed path: a path inside a suite directory names
// that suite; a path inside a contrib module names the suites its mapping
// lists, expanded through the module require graph (a change to a required
// module affects its dependents' suites); a file of the shared _integration
// module itself, a shared-modules entry, or a workspace manifest drives every
// gated lane in full. A path that turns a lane's gate on but names no suite for it fails open: that lane
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

	otelcSuite, err := t.otelcSuites()
	if err != nil {
		return err
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
		sel = map[string]map[string]bool{laneOtelc: {}, laneOrchestrion: {}}
		esc = map[string]bool{laneOtelc: false, laneOrchestrion: false}
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
		if feedsOtelcExternalApp(file) {
			// The external-app job consumes these inputs; the selection says
			// nothing about suites, and the gate vote stays.
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
			// against it, so every gated lane runs every suite.
			escalateLanes(res, file, esc)
			continue
		}
		if m, ok := enclosingModule(byDir, file); ok {
			if sharedModule[m.Dir] {
				// Cross-cutting modules reach suites that never import them
				// (the weavers inject net/http spans into 20+ suites, and the
				// slog aspect links WrapHandler into every slog.New caller),
				// so every gated lane runs the full suite set.
				escalateLanes(res, file, esc)
				continue
			}
			// A manifest change can upgrade an external dependency for every
			// module that requires it, and the require graph follows repository
			// modules only, so the suites a bump reaches are not visible here:
			// fail the gated lanes open. Root manifests are claimed by the
			// universal component, the _integration one by the shared rule
			// above, so only workspace modules reach this check.
			if strings.HasSuffix(file, "/go.mod") || strings.HasSuffix(file, "/go.sum") {
				escalateLanes(res, file, esc)
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
		case esc[lane]:
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

// escalateLanes fails every lane the path's component gates open, without
// narrowing: shared inputs (the _integration module's own files,
// shared-modules entries, workspace manifests) reach suites no graph the
// selector follows can name, so the gated lanes run every suite instead of
// a narrowed set.
func escalateLanes(res *result, file string, esc map[string]bool) {
	for _, lane := range []string{laneOtelc, laneOrchestrion} {
		if res.pathGatesHave(file, lane) {
			esc[lane] = true
		}
	}
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
