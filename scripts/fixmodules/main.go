// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build scripts

package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

var (
	projectRoot string
	verbose     bool
)

var skipModules = []string{
	"github.com/DataDog/dd-trace-go/tools/v2fix",
	"github.com/DataDog/dd-trace-go/_tools",
}

func init() {
	flag.StringVar(&projectRoot, "root", ".", "Path to the project root (default: \".\")")
	flag.BoolVar(&verbose, "verbose", false, "Run in verbose mode (default: false)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go run ./tools/fixmodules -root=<path> <fix-dir>\n")
		flag.PrintDefaults()
	}
}

// These types come from "go help mod edit"
type (
	Module struct {
		Path    string
		Version string
	}

	GoMod struct {
		// dir is the directory where the module lives
		dir string

		Module    ModPath
		Go        string
		Toolchain string
		Require   []Require
		Exclude   []Module
		Replace   []Replace
		Retract   []Retract
	}

	ModPath struct {
		Path       string
		Deprecated string
	}

	Require struct {
		Path     string
		Version  string
		Indirect bool
	}

	Replace struct {
		Old Module
		New Module
	}

	Retract struct {
		Low       string
		High      string
		Rationale string
	}
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Error: <fix-dir> is required and must be a single argument")
		flag.Usage()
		os.Exit(2)
	}
	if projectRoot == "" {
		fmt.Fprintln(os.Stderr, "Error: -root cannot be empty")
		flag.Usage()
		os.Exit(2)
	}

	root, err := filepath.Abs(projectRoot)
	if err != nil {
		log.Fatal(err)
	}
	if resolved, err := os.Readlink(root); err == nil {
		root = resolved
	}

	fixDir := flag.Arg(0)
	fixDir, err = filepath.Abs(fixDir)
	if err != nil {
		log.Fatal(err)
	}
	if resolved, err := os.Readlink(fixDir); err == nil {
		fixDir = resolved
	}

	goVersion := getProjectGoVersion(root)
	debugLog("using go version globally %s\n", goVersion)
	debugLog("finding modules recursively from %s\n", fixDir)

	fixModules, err := findModules(fixDir)
	if err != nil {
		log.Fatal(err)
	}
	allModules, err := findModules(root)
	if err != nil {
		log.Fatal(err)
	}

	deps := newLocalDeps(allModules)

	var targets []GoMod
	for _, modPath := range sortedKeys(fixModules) {
		if slices.Contains(skipModules, modPath) {
			continue
		}
		targets = append(targets, fixModules[modPath])
	}

	// Pass 1: sync the local replace directives of every target go.mod before
	// any go mod tidy runs. A new local module is then wired everywhere at once,
	// and go.mod files are rewritten through modfile instead of go mod edit,
	// which appends standalone replace lines at the end of the file. Keeping
	// all replaces of a module in one sorted replace block makes concurrent
	// otelc PRs change different lines of the block instead of appending to the
	// same trailing lines.
	for _, mod := range targets {
		want := deps.replaces(mod.Module.Path)
		debugLog("syncing module replaces %s\n", mod.Module.Path)
		for _, r := range want {
			debugLog("  %s => %s\n", r.Old.Path, r.New.Path)
		}
		if err := syncReplaces(mod, allModules, want); err != nil {
			log.Fatal(err)
		}
	}

	// Pass 2: tidy each module bottom-up, dependencies before dependents.
	// Alphabetical order fails here: example.com/otelcexternalapp sorts before
	// github.com/DataDog/dd-trace-go/otelc/all/v2, but it needs the requirement
	// lines that only otelc/all's own tidy adds. Re-sync replaces after tidy to
	// drop replaces for unused requirements and keep the replace block last.
	for _, modPath := range topoSortModules(modulePaths(targets), deps.direct) {
		debugLog("tidying module %s\n", modPath)
		mod := fixModules[modPath]
		if err := tidyModule(mod, goVersion); err != nil {
			log.Fatal(err)
		}
		mod, err := readModule(filepath.Join(mod.dir, "go.mod"))
		if err != nil {
			log.Fatal(err)
		}
		allModules[modPath] = mod
		delete(deps.directDeps, modPath)
		if err := syncReplaces(mod, allModules, deps.replaces(modPath)); err != nil {
			log.Fatal(err)
		}
	}
}

func modulePaths(mods []GoMod) []string {
	paths := make([]string, len(mods))
	for i, mod := range mods {
		paths[i] = mod.Module.Path
	}
	return paths
}

// localDeps holds, for each module, the set of repository modules it uses
// directly: local packages imported by the module's Go files (test and tools
// builds included, because findImports reads every .go file) plus its local
// go.mod requirements. The requirement list alone misses a local package that
// a just-added import brings in, before go mod tidy records it.
type localDeps struct {
	mods       map[string]GoMod
	directDeps map[string][]string
}

func newLocalDeps(mods map[string]GoMod) *localDeps {
	return &localDeps{
		mods:       mods,
		directDeps: make(map[string][]string),
	}
}

// direct returns the sorted paths of the repository modules modPath uses
// directly. Results are memoized, because transitive replace resolution walks
// each dependency once per module that reaches it.
func (d *localDeps) direct(modPath string) []string {
	if deps, ok := d.directDeps[modPath]; ok {
		return deps
	}
	mod, ok := d.mods[modPath]
	if !ok {
		d.directDeps[modPath] = nil
		return nil
	}

	files, err := getModGoFiles(mod)
	if err != nil {
		log.Fatal(err)
	}
	imports, err := findImports(files)
	if err != nil {
		log.Fatal(err)
	}

	depSet := make(map[string]bool)
	addLocal := func(importPath string) {
		if importPath == modPath {
			// exclude self
			return
		}
		if _, isLocal := d.mods[importPath]; isLocal {
			depSet[importPath] = true
		}
	}
	for _, im := range imports {
		if importModule, ok := resolveLocalModule(im, d.mods); ok {
			addLocal(importModule)
		}
	}
	for _, require := range mod.Require {
		addLocal(require.Path)
	}

	deps := slices.Sorted(maps.Keys(depSet))
	d.directDeps[modPath] = deps
	return deps
}

// resolveLocalModule returns the longest local module path in mods that equals
// importPath or is a slash-delimited prefix of importPath. Longest-prefix match
// handles both nested repository modules (such as internal/orchestrion/_integration)
// and third-party-named local dependency modules (such as example.com/chidep/v5router).
func resolveLocalModule(importPath string, mods map[string]GoMod) (string, bool) {
	var best string
	for modPath := range mods {
		if importPath == modPath || strings.HasPrefix(importPath, modPath+"/") {
			if len(modPath) > len(best) {
				best = modPath
			}
		}
	}
	return best, best != ""
}

// replaces returns every local replace directive modPath needs, including the
// transitive ones. Replace directives in dependencies don't propagate to the
// parent, so we must add them explicitly. This is needed for transitive test
// dependencies (e.g. instrumentation/testutils/grpc/v2 required by
// contrib/google.golang.org/grpc/v2). The walk follows direct edges, so a
// dependency that only imports a new local module is resolved before any
// go mod tidy has recorded the requirement.
func (d *localDeps) replaces(modPath string) []Replace {
	replacesSet := make(map[string]Replace)
	queue := slices.Clone(d.direct(modPath))
	for _, dep := range queue {
		rep := getLocalReplace(d.mods, modPath, dep)
		replacesSet[rep.Old.Path] = rep
	}
	for i := 0; i < len(queue); i++ {
		dep := queue[i]
		for _, transDep := range d.direct(dep) {
			if transDep == modPath {
				continue
			}
			if _, already := replacesSet[transDep]; already {
				continue
			}
			rep := getLocalReplace(d.mods, modPath, transDep)
			replacesSet[rep.Old.Path] = rep
			queue = append(queue, transDep)
		}
	}

	replaces := make([]Replace, 0, len(replacesSet))
	for _, r := range replacesSet {
		replaces = append(replaces, r)
	}
	slices.SortFunc(replaces, func(a, b Replace) int {
		return cmp.Compare(a.Old.Path, b.Old.Path)
	})
	return replaces
}

// syncReplaces rewrites mod's go.mod so that its local replace directives match
// want, and writes the file back only when its content changed.
func syncReplaces(mod GoMod, mods map[string]GoMod, want []Replace) error {
	goModPath := filepath.Join(mod.dir, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return err
	}
	newData, err := applyReplaceSync(goModPath, data, mods, want)
	if err != nil {
		return err
	}
	if bytes.Equal(data, newData) {
		return nil
	}
	return os.WriteFile(goModPath, newData, 0o644)
}

// applyReplaceSync returns the go.mod content with local replace directives
// synced to want: local replaces that are missing or stale are updated, local
// replaces that are no longer used are dropped. Whenever a module holds more
// than one replace and its replace set changes or it already has a replace
// block, every replace is merged into one alphabetically sorted replace block,
// so two PRs that each add a local module change different lines of the block
// instead of appending to the same trailing lines. Non-local replaces, versioned
// replaces and comments are preserved. The content is returned unchanged when
// nothing needs to change, so untouched modules keep a zero diff.
func applyReplaceSync(name string, data []byte, mods map[string]GoMod, want []Replace) ([]byte, error) {
	f, err := modfile.Parse(name, data, nil)
	if err != nil {
		return nil, err
	}

	drop, add, afterCount := diffLocalReplaces(f, mods, want)
	changed := len(drop) > 0 || len(add) > 0
	sorted := afterCount > 1 && (changed || hasReplaceBlock(f))
	if !sorted && !changed {
		return data, nil
	}
	if err := applyReplaceEdits(f, drop, add, sorted); err != nil {
		return nil, err
	}
	f.Cleanup()
	return modfile.Format(f.Syntax), nil
}

func diffLocalReplaces(f *modfile.File, mods map[string]GoMod, want []Replace) (drop []string, add []Replace, afterCount int) {
	wantSet := make(map[string]Replace, len(want))
	for _, r := range want {
		wantSet[r.Old.Path] = r
	}

	// DropReplace removes every unversioned entry for a path. Keep the count
	// so duplicate entries are removed and replaced with one entry.
	type localReplace struct {
		newPath string
		count   int
	}
	existing := make(map[string]localReplace, len(f.Replace))
	for _, r := range f.Replace {
		if _, isLocal := mods[r.Old.Path]; isLocal && r.Old.Version == "" {
			e := existing[r.Old.Path]
			e.newPath = r.New.Path
			e.count++
			existing[r.Old.Path] = e
		}
	}

	afterCount = len(f.Replace)
	for path, e := range existing {
		w, wanted := wantSet[path]
		if wanted && e.newPath == w.New.Path && e.count == 1 {
			delete(wantSet, path)
			continue
		}
		drop = append(drop, path)
		afterCount -= e.count
	}
	add = slices.SortedFunc(maps.Values(wantSet), func(a, b Replace) int {
		return cmp.Compare(a.Old.Path, b.Old.Path)
	})
	return drop, add, afterCount + len(add)
}

func applyReplaceEdits(f *modfile.File, drop []string, add []Replace, sorted bool) error {
	for _, path := range drop {
		_ = f.DropReplace(path, "")
	}
	if sorted {
		canonicalizeReplaceBlock(f, add)
		return nil
	}
	for _, r := range add {
		if err := f.AddReplace(r.Old.Path, "", r.New.Path, ""); err != nil {
			return err
		}
	}
	return nil
}

// hasReplaceBlock reports whether any replace directive of f sits in a
// parenthesized replace block.
func hasReplaceBlock(f *modfile.File) bool {
	for _, stmt := range f.Syntax.Stmt {
		if block, ok := stmt.(*modfile.LineBlock); ok && isReplaceStmt(block) {
			return true
		}
	}
	return false
}

// isReplaceStmt reports whether stmt is a replace directive, standalone or in a
// block.
func isReplaceStmt(stmt modfile.Expr) bool {
	switch stmt := stmt.(type) {
	case *modfile.Line:
		return len(stmt.Token) > 0 && stmt.Token[0] == "replace"
	case *modfile.LineBlock:
		return len(stmt.Token) > 0 && stmt.Token[0] == "replace"
	}
	return false
}

// canonicalizeReplaceBlock rewrites every replace directive of f, plus the
// directives in add, into a single replace block sorted by old path, then old
// version. Comments stay attached to their directive. Replaces that are not
// local modules are kept.
func canonicalizeReplaceBlock(f *modfile.File, add []Replace) {
	comments := collectReplaceBlockComments(f)
	entries := make([]replaceBlockEntry, 0, len(f.Replace)+len(add))
	for _, r := range f.Replace {
		if r.Old.Path != "" {
			entries = append(entries, replaceBlockEntry{
				old: r.Old, new: r.New, comments: r.Syntax.Comments,
			})
		}
	}
	for _, r := range add {
		entries = append(entries, replaceBlockEntry{
			old: module.Version{Path: r.Old.Path},
			new: module.Version{Path: r.New.Path},
		})
	}
	slices.SortFunc(entries, func(a, b replaceBlockEntry) int {
		if c := cmp.Compare(a.old.Path, b.old.Path); c != 0 {
			return c
		}
		return cmp.Compare(a.old.Version, b.old.Version)
	})
	buildReplaceBlock(f, entries, comments)
}

type replaceBlockEntry struct {
	old, new module.Version
	comments modfile.Comments
}

type replaceBlockComments struct {
	block, lparen, rparen modfile.Comments
}

func collectReplaceBlockComments(f *modfile.File) replaceBlockComments {
	// Comments on the block, its parentheses, or after a standalone line
	// describe the replace section rather than one directive.
	var comments replaceBlockComments
	for _, r := range f.Replace {
		if r.Old.Path != "" && !r.Syntax.InBlock {
			comments.block.After = append(comments.block.After, r.Syntax.After...)
		}
	}
	for _, stmt := range f.Syntax.Stmt {
		if block, ok := stmt.(*modfile.LineBlock); ok && isReplaceStmt(block) {
			comments.block.Before = append(comments.block.Before, block.Before...)
			comments.block.Suffix = append(comments.block.Suffix, block.Suffix...)
			comments.block.After = append(comments.block.After, block.After...)
			comments.lparen.Before = append(comments.lparen.Before, block.LParen.Before...)
			comments.lparen.Suffix = append(comments.lparen.Suffix, block.LParen.Suffix...)
			comments.lparen.After = append(comments.lparen.After, block.LParen.After...)
			comments.rparen.Before = append(comments.rparen.Before, block.RParen.Before...)
			comments.rparen.Suffix = append(comments.rparen.Suffix, block.RParen.Suffix...)
			comments.rparen.After = append(comments.rparen.After, block.RParen.After...)
		}
	}
	return comments
}

func buildReplaceBlock(f *modfile.File, entries []replaceBlockEntry, comments replaceBlockComments) {
	for _, r := range f.Replace {
		if r.Old.Path != "" {
			_ = f.DropReplace(r.Old.Path, r.Old.Version)
		}
	}
	block := &modfile.LineBlock{
		Comments: modfile.Comments{
			Before: comments.block.Before,
			Suffix: comments.block.Suffix,
			After:  comments.block.After,
		},
		Token:  []string{"replace"},
		LParen: modfile.LParen{Comments: comments.lparen},
		RParen: modfile.RParen{Comments: comments.rparen},
	}
	f.Replace = f.Replace[:0]
	for _, e := range entries {
		tokens := []string{modfile.AutoQuote(e.old.Path)}
		if e.old.Version != "" {
			tokens = append(tokens, e.old.Version)
		}
		tokens = append(tokens, "=>", modfile.AutoQuote(e.new.Path))
		if e.new.Version != "" {
			tokens = append(tokens, e.new.Version)
		}
		line := &modfile.Line{
			Comments: modfile.Comments{Before: e.comments.Before, Suffix: e.comments.Suffix},
			Token:    tokens,
			InBlock:  true,
		}
		block.Line = append(block.Line, line)
		f.Replace = append(f.Replace, &modfile.Replace{
			Old:    e.old,
			New:    e.new,
			Syntax: line,
		})
	}
	// Keep the replace block at the end of go.mod (after require blocks), so
	// go mod tidy inserts new requires into the sorted require blocks instead of
	// creating trailing require blocks below replace directives.
	f.Syntax.Stmt = append(f.Syntax.Stmt, block)
}

// topoSortModules returns the module paths ordered bottom-up for tidying: a
// module comes after every listed module it uses directly, so dependencies are
// tidied before their dependents import their packages (contrib/<x>/otelc
// before otelc/all, otelc/all before example.com/otelcexternalapp). Ties break
// alphabetically by module path. Modules outside paths impose no order.
// Strongly connected components (cycles) are condensed into a DAG so cyclic
// dependencies still come before any module outside the cycle that depends on
// them; members inside a cycle are ordered alphabetically.
func topoSortModules(paths []string, direct func(string) []string) []string {
	sorted := slices.Sorted(slices.Values(paths))
	adj := buildModuleDepGraph(sorted, direct)
	sccs, compOf := tarjanSCCs(sorted, adj)
	degree, dependents := condenseSCCGraph(sorted, adj, compOf, len(sccs))
	return kahnOrderSCCs(sccs, degree, dependents, len(sorted))
}

func buildModuleDepGraph(sorted []string, direct func(string) []string) map[string][]string {
	inList := make(map[string]bool, len(sorted))
	for _, p := range sorted {
		inList[p] = true
	}

	// Build filtered dependency adjacency list: adj[p] = local deps of p in paths.
	adj := make(map[string][]string, len(sorted))
	for _, p := range sorted {
		seen := make(map[string]bool)
		for _, dep := range direct(p) {
			if !inList[dep] || dep == p || seen[dep] {
				continue
			}
			seen[dep] = true
			adj[p] = append(adj[p], dep)
		}
		slices.Sort(adj[p])
	}

	return adj
}

func tarjanSCCs(sorted []string, adj map[string][]string) ([][]string, map[string]int) {
	var (
		nextIndex int
		index     = make(map[string]int, len(sorted))
		lowlink   = make(map[string]int, len(sorted))
		onStack   = make(map[string]bool, len(sorted))
		stack     []string
		sccs      [][]string
		compOf    = make(map[string]int, len(sorted))
	)
	var strongConnect func(v string)
	strongConnect = func(v string) {
		nextIndex++
		index[v] = nextIndex
		lowlink[v] = nextIndex
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range adj[v] {
			if index[w] == 0 {
				strongConnect(w)
				lowlink[v] = min(lowlink[v], lowlink[w])
			} else if onStack[w] {
				lowlink[v] = min(lowlink[v], index[w])
			}
		}

		if lowlink[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			slices.Sort(comp)
			id := len(sccs)
			for _, m := range comp {
				compOf[m] = id
			}
			sccs = append(sccs, comp)
		}
	}
	for _, p := range sorted {
		if index[p] == 0 {
			strongConnect(p)
		}
	}

	return sccs, compOf
}

func condenseSCCGraph(sorted []string, adj map[string][]string, compOf map[string]int, count int) ([]int, [][]int) {
	// Edges point from each dependency to its dependents.
	degree := make([]int, count)
	dependents := make([][]int, count)
	seenEdge := make(map[[2]int]bool)
	for _, p := range sorted {
		cp := compOf[p]
		for _, dep := range adj[p] {
			cd := compOf[dep]
			if cp == cd {
				continue
			}
			edge := [2]int{cd, cp}
			if seenEdge[edge] {
				continue
			}
			seenEdge[edge] = true
			degree[cp]++
			dependents[cd] = append(dependents[cd], cp)
		}
	}

	return degree, dependents
}

func kahnOrderSCCs(sccs [][]string, degree []int, dependents [][]int, pathCount int) []string {
	// Break ties by the smallest path in each component.
	cmpComp := func(a, b int) int {
		return cmp.Compare(sccs[a][0], sccs[b][0])
	}
	var available []int
	for id := range len(sccs) {
		if degree[id] == 0 {
			available = append(available, id)
		}
	}
	slices.SortFunc(available, cmpComp)

	order := make([]string, 0, pathCount)
	for len(available) > 0 {
		id := available[0]
		available = available[1:]
		order = append(order, sccs[id]...)
		for _, depComp := range dependents[id] {
			degree[depComp]--
			if degree[depComp] == 0 {
				i, _ := slices.BinarySearchFunc(available, depComp, cmpComp)
				available = slices.Insert(available, i, depComp)
			}
		}
	}
	return order
}

// tidyModule runs go mod tidy on mod and removes any toolchain directive that
// the tidy added.
func tidyModule(mod GoMod, goVersion string) error {
	cmd := exec.Command("go", "mod", "tidy", "-v", "-go", goVersion)
	cmd.Stderr = os.Stderr
	cmd.Dir = mod.dir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("'go mod tidy' failed for %s: %w", mod.Module.Path, err)
	}

	cmd = exec.Command("go", "mod", "edit", "-toolchain", "none")
	cmd.Stderr = os.Stderr
	cmd.Dir = mod.dir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("'go mod edit' toolchain failed for %s: %w", mod.Module.Path, err)
	}
	return nil
}

func getLocalReplace(mods map[string]GoMod, mod, require string) Replace {
	mDir := mods[mod].dir
	rDir := mods[require].dir

	rel, err := filepath.Rel(mDir, rDir)
	if err != nil {
		log.Fatal(err)
	}
	if !strings.HasPrefix(rel, "./") && !strings.HasPrefix(rel, "../") {
		rel = "./" + rel
	}
	return Replace{
		Old: Module{
			Path: require,
		},
		New: Module{
			Path:    rel,
			Version: "",
		},
	}
}

func readModule(path string) (GoMod, error) {
	cmd := exec.Command("go", "mod", "edit", "-json", path)
	cmd.Stderr = os.Stderr
	output, err := cmd.Output()
	if err != nil {
		return GoMod{}, err
	}
	m := GoMod{dir: filepath.Dir(path)}
	if err := json.Unmarshal(output, &m); err != nil {
		return GoMod{}, err
	}
	return m, nil
}

func sortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	return slices.Sorted(maps.Keys(m))
}

func findModules(root string) (map[string]GoMod, error) {
	modules := make(map[string]GoMod)
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if info.Name() == "go.mod" {
				m, err := readModule(path)
				if err != nil {
					return err
				}
				modules[m.Module.Path] = m
			}
			return nil
		}
		if name := info.Name(); name != root && strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return modules, nil
}

func getModGoFiles(mod GoMod) ([]string, error) {
	var goFiles []string
	var nestedMods []string
	// The walk starts at mod.dir and reports the module's own go.mod at
	// exactly this path. Comparing the path instead of relying on walk order
	// matters: a nested module directory (e.g. "aaa/") can sort before the
	// parent's go.mod, so the walk may meet a nested go.mod first.
	ownGoMod := filepath.Join(mod.dir, "go.mod")

	err := filepath.WalkDir(mod.dir, func(path string, f fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if f.Name() == "go.mod" {
			if path == ownGoMod {
				return nil
			}
			// A nested module. SkipDir on a file skips the remaining entries
			// of its directory, and the walk continues with the next sibling,
			// so every nested module is recorded and no parent file is dropped.
			nestedMods = append(nestedMods, filepath.Dir(path))
			return fs.SkipDir
		}
		if filepath.Ext(path) == ".go" {
			goFiles = append(goFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// A nested module's .go files that sort before its go.mod are collected
	// before the module is discovered, so filter them out by path here. The
	// separator keeps a merely similarly-named sibling directory (e.g.
	// "testdata/broken" vs "testdata/brokenimport") distinguishable from the
	// nested module's own files.
	shouldInclude := func(path string) bool {
		for _, nm := range nestedMods {
			if strings.HasPrefix(path, nm+string(filepath.Separator)) {
				return false
			}
		}
		return true
	}
	var filtered []string
	for _, f := range goFiles {
		if shouldInclude(f) {
			filtered = append(filtered, f)
		}
	}
	return filtered, nil
}

func findImports(goFiles []string) ([]string, error) {
	importSet := make(map[string]bool)
	fset := token.NewFileSet()

	for _, goFile := range goFiles {
		f, err := parser.ParseFile(fset, goFile, nil, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.IMPORT {
				continue
			}
			for _, spec := range genDecl.Specs {
				importSpec := spec.(*ast.ImportSpec)

				importSet[strings.Trim(importSpec.Path.Value, "\"")] = true
			}
		}
	}

	return slices.Sorted(maps.Keys(importSet)), nil
}

func getProjectGoVersion(root string) string {
	b, err := os.ReadFile(path.Join(root, "go.mod"))
	if err != nil {
		log.Fatal(err)
	}
	f, err := modfile.Parse("go.mod", b, nil)
	if err != nil {
		panic(err)
	}
	return f.Go.Version
}

func debugLog(format string, v ...any) {
	if !verbose {
		return
	}
	log.Printf(format, v...)
}
