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
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const goVersion = "1.21"

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s <dir>\n", os.Args[0])
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
	log.Printf("finding modules recursively from %s\n", root)

	modules := make(map[string]*GoMod)
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
		log.Fatal(err)
	}

	for _, modPath := range sortedKeys(modules) {
		mod := modules[modPath]

		var replaces []Replace
		for _, require := range mod.Require {
			// it's a local module
			_, ok := modules[require.Path]
			if ok {
				replaces = append(replaces, getLocalReplace(modules, modPath, require.Path))
			}
		}
		log.Printf("fixing module: %s", modPath)
		if err := fixModule(modules, mod, replaces); err != nil {
			log.Fatal(err)
		}
	}
}

func getLocalReplace(mods map[string]*GoMod, mod, require string) Replace {
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

func readModule(path string) (*GoMod, error) {
	cmd := exec.Command("go", "mod", "edit", "-json", path)
	cmd.Stderr = os.Stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	m := GoMod{dir: filepath.Dir(path)}
	if err := json.Unmarshal(output, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func fixModule(mods map[string]*GoMod, mod *GoMod, replaces []Replace) error {
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
