// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// This program generates the `orchestrion.tool.go` file at `./orchestrion/all`
// from the root of the repository, which contains the necessary directives to
// facilitate onboarding of orchestrion. The `orchestrion.tool.go` file contains
// an import directive for every package in `dd-trace-go` that contains an
// `orchestrion.yml` file.
//
// Orchestrion uses this file when users import
// "github.com/DataDog/dd-trace-go/orchestrion/all` in their application's
// `orchestrion.tool.go` file, intending to enable every available feature of
// the tracer library.
package main

import (
	"bytes"
	"encoding/json"
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

	"github.com/DataDog/dd-trace-go/v2/internal/version"
	"golang.org/x/tools/go/packages"

	_ "embed" // For go:embed
)

const orchestrionToolGo = "orchestrion.tool.go"

var (
	//go:embed orchestrion.tool.go.tmpl
	templateText string
	fileTemplate = template.Must(template.New(orchestrionToolGo).Parse(templateText))

	//go:embed go.mod.tmpl
	goModTemplateText string
	goModTemplate     = template.Must(template.New("go.mod").Parse(goModTemplateText))
)

func main() {
	_, thisFile, _, _ := runtime.Caller(0)
	rootDir := filepath.Join(thisFile, "..", "..", "..", "..")

	var buf bytes.Buffer
	cmd := exec.Command("go", "list", "-m", "--versions", `-f={{ $v := "" }}{{ range .Versions }}{{ $v = . }}{{ end }}{{ $v }}`, "github.com/DataDog/orchestrion")
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalln(err)
	}

	modules, err := generateRootConfig(rootDir, strings.TrimSpace(buf.String()))
	if err != nil {
		log.Fatalln(err)
	}

	if err := validateValidConfig(modules); err != nil {
		log.Fatalln(err)
	}
}

func generateRootConfig(rootDir string, orchestrionLatestVersion string) (map[string]string, error) {
	var (
		paths   = []string{"github.com/DataDog/dd-trace-go/v2/orchestrion"} // Allows access to the `/internal/` stuff such as CI Viz.
		modules = make(map[string]string)
	)
	err := filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			// Ignore directories the go toolchain normally ignores.
			if entry.Name() == "internal" || entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), "_") || strings.HasPrefix(entry.Name(), ".") {
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

		pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedModule, Dir: rootDir}, "./"+rel)
		if err != nil {
			log.Fatalln(err)
		}

		paths = append(paths, pkgs[0].PkgPath)
		modules[pkgs[0].Module.Path] = pkgs[0].Module.Dir

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing packages to import: %w", err)
	}
	// Sort to ensure consistent ordering...
	slices.Sort(paths)

	var buf bytes.Buffer
	if err := fileTemplate.Execute(&buf, paths); err != nil {
		return nil, fmt.Errorf("rendering Go code template: %w", err)
	}

	src, err := format.Source(buf.Bytes())
	if err != nil {
		log.Fatalln(err)
	}

	pkgDir := filepath.Join(rootDir, "orchestrion", "all")

	err = os.WriteFile(
		filepath.Join(pkgDir, orchestrionToolGo),
		src,
		0o644,
	)
	if err != nil {
		return nil, fmt.Errorf("writing "+orchestrionToolGo+" file: %w", err)
	}

	goVersion, err := getLanguageLevel(rootDir)
	if err != nil {
		return nil, err
	}

	replaces, err := makeRelative(modules, pkgDir)
	if err != nil {
		return nil, err
	}
	var goMod bytes.Buffer
	if err := goModTemplate.Execute(&goMod, map[string]any{
		"GoVersion":         goVersion,
		"OrchestrionLatest": orchestrionLatestVersion,
		"Modules":           replaces,
		"VersionTag":        version.Tag,
	}); err != nil {
		return nil, fmt.Errorf("rendering go.mod from template: %w", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "go.mod"), goMod.Bytes(), 0o644); err != nil {
		return nil, fmt.Errorf("writing go.mod: %w", err)
	}

	if err := goCmd(pkgDir, "mod", "tidy", "-go", goVersion); err != nil {
		return nil, fmt.Errorf("go mod tidy: %w", err)
	}

	// Make sure this is present in the modules map, as it's not a natural part of it...
	modules["github.com/DataDog/dd-trace-go/orchestrion/all/v2"] = pkgDir
	return modules, nil
}

func getLanguageLevel(dir string) (string, error) {
	cmd := exec.Command("go", "mod", "edit", "-json")
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf

	if err := cmd.Run(); err != nil {
		return "", err
	}

	var res struct {
		Go string `json:"Go"`
	}

	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		return "", err
	}

	return res.Go, nil
}

func makeRelative(modules map[string]string, toDir string) ([][2]string, error) {
	res := make([][2]string, 0, len(modules))
	for name, path := range modules {
		rel, err := filepath.Rel(toDir, path)
		if err != nil {
			return nil, err
		}
		res = append(res, [2]string{name, rel})
	}

	slices.SortFunc(res, func(l, r [2]string) int {
		return strings.Compare(l[0], r[0])
	})
	return res, nil
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
	_ "github.com/DataDog/dd-trace-go/orchestrion/all/v2" // integration
)
`
)

func validateValidConfig(modules map[string]string) error {
	tmp, err := os.MkdirTemp("", "dd-trace-go.orchestrion-*")
	if err != nil {
		return fmt.Errorf("MkdirTemp: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := goCmd(tmp, "mod", "init", "github.com/DataDog/dd-trace-go.orchestrion"); err != nil {
		return fmt.Errorf("init module: %w", err)
	}
	mods := []string{"edit"}
	for name, path := range modules {
		mods = append(mods, "-require", name+"@"+version.Tag, "-replace", name+"="+path)
	}
	if err := goCmd(tmp, "mod", mods...); err != nil {
		return fmt.Errorf("go mod %s: %w", mods, err)
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

	logFile := filepath.Join(tmp, "orchestrion.log")
	fmt.Println("Orchestrion log file is:", logFile)
	if err := goCmd(tmp, "run",
		"github.com/DataDog/orchestrion", "-log-level=trace", "-log-file", logFile,
		"go", "run", ".",
	); err != nil {
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
