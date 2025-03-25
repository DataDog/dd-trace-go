// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"text/template"

	"golang.org/x/mod/modfile"
)

const tmpl = `module {{.ImportPath}}

go 1.20

require (
	{{.DependencyPath}} {{.DependencyVersion}}
)
`

func main() {
	if len(os.Args) != 3 {
		panic("usage: go run main.go <contrib-path> <dependency-path>")
	}
	var (
		contribPath    = path.Clean(os.Args[1])
		dependencyPath = os.Args[2]
	)
	if err := generateGoMod(contribPath, dependencyPath); err != nil {
		panic(err)
	}
	pwd, _ := os.Getwd()
	if err := os.Chdir(contribPath); err != nil {
		panic(err)
	}
	commitMsg := fmt.Sprintf("%s: add go.mod", contribPath)
	if err := commit(commitMsg); err != nil {
		panic(err)
	}
	if err := push(revParse("HEAD")); err != nil {
		panic(err)
	}
	if err := goGetV2(); err != nil {
		panic(err)
	}
	if err := modTidy(); err != nil {
		panic(err)
	}
	commitMsg = fmt.Sprintf("%s: update go.mod", contribPath)
	if err := os.Chdir(pwd); err != nil {
		panic(err)
	}
	if err := moveContrib(contribPath); err != nil {
		panic(err)
	}
	if err := modTidy(); err != nil {
		panic(err)
	}
	if err := commit(commitMsg); err != nil {
		panic(err)
	}
	if err := push(""); err != nil {
		panic(err)
	}
}

func generateGoMod(contribDir, dependencyPath string) error {
	// Build the v2 import path for the contrib package.
	importPath := fmt.Sprintf("github.com/DataDog/dd-trace-go/%s/v2", contribDir)

	// Resolve the dependency version from the go.mod file.
	var (
		buf               bytes.Buffer
		dependencyVersion string
	)
	mod, err := os.Open("go.mod")
	if err != nil {
		return err
	}
	defer mod.Close()
	if _, err = buf.ReadFrom(mod); err != nil {
		return err
	}
	deps, err := modfile.ParseLax("go.mod", buf.Bytes(), nil)
	if err != nil {
		return err
	}
	for _, r := range deps.Require {
		if r.Mod.Path == dependencyPath {
			dependencyVersion = r.Mod.Version
			break
		}
	}
	if dependencyVersion == "" {
		return fmt.Errorf("dependency %s not found in go.mod", dependencyPath)
	}
	// Write the go.mod file to dst.
	dstPath := path.Join(contribDir, "go.mod")
	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	t := template.Must(template.New("go.mod").Parse(tmpl))
	return t.Execute(dst, struct {
		DependencyPath    string
		DependencyVersion string
		ImportPath        string
	}{
		DependencyPath:    dependencyPath,
		DependencyVersion: dependencyVersion,
		ImportPath:        importPath,
	})
}

func goGetV2() error {
	currentBranch := revParse("HEAD")
	importPath := fmt.Sprintf("github.com/DataDog/dd-trace-go/v2@%s", currentBranch)
	cmd := exec.Command("go", "get", "-u", importPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func commit(message string) error {
	cmd := exec.Command("git", "add", "-A")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	cmd = exec.Command("git", "commit", "-m", message)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func push(upstream string) error {
	var cmd *exec.Cmd
	if upstream == "" {
		cmd = exec.Command("git", "push")
	} else {
		cmd = exec.Command("git", "push", "--set-upstream", "origin", upstream)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func revParse(ref string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", ref)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic(err)
	}
	return strings.TrimSpace(buf.String())
}

func moveContrib(contribPath string) error {
	dst := fmt.Sprintf("v2/%s", contribPath)
	p := strings.Split(dst, "/")
	c := path.Join(p[:len(p)-1]...)
	if err := os.MkdirAll(c, 0755); err != nil {
		return err
	}
	if err := os.Rename(contribPath, dst); err != nil {
		return err
	}
	p = strings.Split(contribPath, "/")
	c = path.Join(p[:len(p)-1]...)
	_ = os.Remove(c)
	return nil
}

func modTidy() error {
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
