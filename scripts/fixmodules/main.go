// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build scripts
// +build scripts

package main

import (
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
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

	for _, modPath := range sortedKeys(fixModules) {
		mod := fixModules[modPath]
		if slices.Contains(skipModules, mod.Module.Path) {
			continue
		}

		files, err := getModGoFiles(mod)
		if err != nil {
			log.Fatal(err)
		}
		imports, err := findImports(files)
		if err != nil {
			log.Fatal(err)
		}

		replacesSet := make(map[string]Replace)
		for _, im := range imports {
			// the module name and the import path might be different (when the imported package is a sub-package)
			importModule := im
			if strings.HasPrefix(im, "github.com/DataDog/dd-trace-go") {
				if left, _, ok := strings.Cut(im, "/v2"); ok {
					importModule = left + "/v2"
				}
			}
			if importModule == mod.Module.Path {
				// exclude self
				continue
			}
			// it's a local module
			_, ok := allModules[importModule]
			if ok {
				rep := getLocalReplace(allModules, modPath, importModule)
				replacesSet[rep.Old.Path] = rep
			}
		}
		for _, require := range mod.Require {
			// it's a local module
			_, ok := allModules[require.Path]
			if ok {
				rep := getLocalReplace(allModules, modPath, require.Path)
				replacesSet[rep.Old.Path] = rep
			}
		}
		var replaces []Replace
		for _, r := range replacesSet {
			replaces = append(replaces, r)
		}
		slices.SortFunc(replaces, func(a, b Replace) int {
			return cmp.Compare(a.Old.Path, b.Old.Path)
		})

		debugLog("adding module replaces %q\n", modPath)
		for _, r := range replaces {
			debugLog("  %s => %s\n", r.Old.Path, r.New.Path)
		}
		if err := fixModule(allModules, mod, goVersion, replaces); err != nil {
			log.Fatal(err)
		}
	}
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

func fixModule(mods map[string]GoMod, mod GoMod, goVersion string, replaces []Replace) error {
	var drop []Replace
	add := append([]Replace{}, replaces...)

	// first, clean previous local replaces
	for _, existingReplace := range mod.Replace {
		if _, ok := mods[existingReplace.Old.Path]; ok {
			found := false
			for i, newReplace := range add {
				if newReplace == existingReplace {
					found = true
					add = append(add[:i], add[i+1:]...)
					break
				}
			}
			if !found {
				drop = append(drop, existingReplace)
			}
		}
	}

	if len(drop) > 0 {
		var args []string
		args = append(args, "mod", "edit")
		for _, replace := range drop {
			args = append(args, fmt.Sprintf("-dropreplace=%s", replace.Old.Path))
		}
		cmd := exec.Command("go", args...)
		cmd.Stderr = os.Stderr
		cmd.Dir = mod.dir
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("'go mod edit' dropreplace failed: %w", err)
		}
	}

	if len(add) > 0 {
		var args []string
		args = append(args, "mod", "edit")
		for _, replace := range add {
			args = append(args, fmt.Sprintf("-replace=%s=%s", replace.Old.Path, replace.New.Path))
		}
		cmd := exec.Command("go", args...)
		cmd.Stderr = os.Stderr
		cmd.Dir = mod.dir
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("command %q failed: %w", cmd.String(), err)
		}
	}

	cmd := exec.Command("go", "mod", "tidy", "-v", "-go", goVersion)
	cmd.Stderr = os.Stderr
	cmd.Dir = mod.dir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("'go mod tidy' failed: %w", err)
	}

	cmd = exec.Command("go", "mod", "edit", "-toolchain", "none")
	cmd.Stderr = os.Stderr
	cmd.Dir = mod.dir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("'go mod edit' toolchain failed: %w", err)
	}

	return nil
}

func sortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	var keys []K
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
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
	foundGoMod := false

	err := filepath.WalkDir(mod.dir, func(path string, f fs.DirEntry, err error) error {
		if f.Name() == "go.mod" {
			if foundGoMod {
				nestedMods = append(nestedMods, filepath.Dir(path))
				return fs.SkipAll
			}
			foundGoMod = true
		}
		if filepath.Ext(path) == ".go" {
			goFiles = append(goFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	shouldInclude := func(path string) bool {
		for _, nm := range nestedMods {
			if strings.HasPrefix(path, nm) {
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

	var imports []string
	for k := range importSet {
		imports = append(imports, k)
	}
	sort.Strings(imports)
	return imports, nil
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
