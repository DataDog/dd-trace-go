// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

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
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const goVersion = "1.21"

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s <root> [fix-dir]\n", os.Args[0])
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
	if flag.NArg() < 1 || flag.NArg() > 2 {
		flag.Usage()
		os.Exit(2)
	}

	root, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	if resolved, err := os.Readlink(root); err == nil {
		root = resolved
	}
	fixDir := flag.Arg(1)
	if fixDir == "" {
		fixDir = root
	}
	fixDir, err = filepath.Abs(fixDir)
	if err != nil {
		log.Fatal(err)
	}
	if resolved, err := os.Readlink(fixDir); err == nil {
		fixDir = resolved
	}

	log.Printf("finding modules recursively from %s\n", fixDir)

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

		log.Printf("fixing module: %s", modPath)
		log.Printf("  need replaces: %v", replaces)
		if err := fixModule(allModules, mod, replaces); err != nil {
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

func fixModule(mods map[string]GoMod, mod GoMod, replaces []Replace) error {
	// first, clean previous local replaces
	for _, replace := range mod.Replace {
		if _, ok := mods[replace.Old.Path]; ok {
			args := []string{
				"mod",
				"edit",
				fmt.Sprintf("-dropreplace=%s", replace.Old.Path),
			}
			cmd := exec.Command("go", args...)
			cmd.Stderr = os.Stderr
			cmd.Dir = mod.dir
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("'go mod edit' dropreplace failed: %w", err)
			}
		}
	}

	// after cleaning up, add the necessary local replaces
	for _, replace := range replaces {
		args := []string{
			"mod",
			"edit",
			fmt.Sprintf("-replace=%s=%s", replace.Old.Path, replace.New.Path),
		}
		cmd := exec.Command("go", args...)
		cmd.Stderr = os.Stderr
		cmd.Dir = mod.dir
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("'go mod edit' replace failed: %w", err)
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
	foundGoMod := false

	err := filepath.WalkDir(mod.dir, func(path string, d fs.DirEntry, err error) error {
		if d.Name() == "go.mod" {
			if foundGoMod {
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
	return goFiles, nil
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
	for k, _ := range importSet {
		imports = append(imports, k)
	}
	sort.Strings(imports)
	return imports, nil
}
