// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"text/template"

	_ "embed" // For go:embed

	"golang.org/x/tools/go/packages"
)

const orchestrionToolGo = "orchestrion.tool.go"

var (
	//go:embed orchestrion.tool.go.tmpl
	templateText string
	fileTemplate = template.Must(template.New(orchestrionToolGo).Parse(templateText))
)

func main() {
	_, thisFile, _, _ := runtime.Caller(0)
	rootDir := filepath.Join(thisFile, "..", "..", "..", "..")

	if err := generateRootYAML(rootDir); err != nil {
		log.Fatalln(err)
	}

	if err := validateValidConfig(rootDir); err != nil {
		log.Fatalln(err)
	}
}

func generateRootYAML(rootDir string) error {
	var paths []string
	err := filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			// Ignore directories the go toolchain normally ignores.
			if entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), "_") || strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}

		if entry.Name() != "orchestrion.yml" {
			return nil
		}

		rel, err := filepath.Rel(rootDir, filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("relative path of %q: %w", path, err)
		}

		if rel == "." {
			// We don't want to have the root file circular reference itself!
			return nil
		}

		pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName, Dir: rootDir}, "./"+rel)
		if err != nil {
			log.Fatalln(err)
		}

		paths = append(paths, pkgs[0].PkgPath)

		return nil
	})
	if err != nil {
		return fmt.Errorf("listing YAML documents to extend: %w", err)
	}

	// Sort to ensure consistent ordering...
	slices.Sort(paths)

	var buf bytes.Buffer
	if err := fileTemplate.Execute(&buf, paths); err != nil {
		return fmt.Errorf("rendering YAML template: %w", err)
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		log.Fatalln(err)
	}

	err = os.WriteFile(
		filepath.Join(rootDir, orchestrionToolGo),
		src,
		0o644,
	)
	if err != nil {
		return fmt.Errorf("writing YAML file: %w", err)
	}

	return nil
}

const (
	mainGo = `package main

import (
	"log"

	"github.com/DataDog/orchestrion/runtime/built"
)

func main(){
	if !built.WithOrchestrion {
		log.Fatalln("Not built with orchestrion ☹️")
	}
}
`

	orchestrionToolGoContent = `//go:build tools
package tools

import (
	_ "github.com/DataDog/orchestrion"
	_ "gopkg.in/DataDog/dd-trace-go.v1" // integration
)
`
)

func validateValidConfig(rootDir string) error {
	tmp, err := os.MkdirTemp("", "dd-trace-go.orchestrion-*")
	if err != nil {
		return fmt.Errorf("MkdirTemp: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := goCmd(tmp, "mod", "init", "github.com/DataDog/dd-trace-go.orchestrion"); err != nil {
		return fmt.Errorf("init module: %w", err)
	}
	if err := goCmd(tmp, "mod", "edit", "-replace", "gopkg.in/DataDog/dd-trace-go.v1="+rootDir); err != nil {
		return fmt.Errorf("replace gopkg.in/DataDog/dd-trace-go.v1: %w", err)
	}
	// TODO: Remove before shipping
	if err := goCmd(tmp, "mod", "edit", "-require", "github.com/DataDog/orchestrion@v1.0.3-rc.1.0.20250109145419-86f02c486a31"); err != nil {
		return fmt.Errorf("replace gopkg.in/DataDog/dd-trace-go.v1: %w", err)
	}

	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(mainGo), 0o644); err != nil {
		return fmt.Errorf("writing main.go: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, orchestrionToolGo), []byte(orchestrionToolGoContent), 0o644); err != nil {
		return fmt.Errorf("writing "+orchestrionToolGo+": %w", err)
	}

	if err := goCmd(tmp, "mod", "tidy"); err != nil {
		return fmt.Errorf("go mod tidy: %w", err)
	}

	if err := goCmd(tmp, "run", "github.com/DataDog/orchestrion", "go", "run", "."); err != nil {
		return fmt.Errorf("go run: %w", err)
	}

	return nil
}

func goCmd(dir string, command string, args ...string) error {
	cmd := exec.Command("go", command)
	cmd.Args = append(cmd.Args, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
