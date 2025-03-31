// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

//go:build ignore
// +build ignore

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

const netHTTPPath = "github.com/DataDog/dd-trace-go/contrib/net/http/v2"

type pinning struct {
	headCommit string
}

func (p *pinning) loadHeadCommit() {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		panic(err)
	}
	p.headCommit = strings.TrimSpace(string(out))
	p.headCommit = p.headCommit[:12]
	fmt.Println("# HEAD commit:", p.headCommit)
}

func (p pinning) pin(pattern, goVersion string) {
	contribs, err := filepath.Glob(pattern)
	if err != nil {
		panic(err)
	}
	for _, contrib := range contribs {
		pwd, _ := os.Getwd()
		d := filepath.Dir(contrib)
		if err = os.Chdir(d); err != nil {
			panic(err)
		}
		fmt.Printf("# %s\n", d)
		mods, err := p.loadOutdatedDDTraceMods()
		if err != nil {
			panic(err)
		}
		for _, mod := range mods {
			if err = pinMod(mod); err != nil {
				panic(err)
			}
		}
		if err = modTidy(goVersion); err != nil {
			panic(err)
		}
		if err = os.Chdir(pwd); err != nil {
			panic(err)
		}
		fmt.Println()
	}
}

func (p pinning) loadOutdatedDDTraceMods() ([]string, error) {
	mod, err := os.Open("go.mod")
	if err != nil {
		return nil, err
	}
	defer mod.Close()
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(mod); err != nil {
		return nil, err
	}
	deps, err := modfile.ParseLax("go.mod", buf.Bytes(), nil)
	if err != nil {
		return nil, err
	}
	mods := make([]string, 0, 2) // On average, we expect to pin 1 or 2 modules, so we start with a small capacity.
	foundNetHTTP := false
	for _, dep := range deps.Require {
		if !strings.HasPrefix(dep.Mod.Path, "github.com/DataDog/dd-trace-go/v2") {
			continue
		}
		if dep.Mod.Path == netHTTPPath {
			foundNetHTTP = true
		}
		// Skip if the module is already pinned to the head commit.
		if strings.HasSuffix(dep.Mod.Version, p.headCommit) {
			continue
		}
		mods = append(mods, dep.Mod.Path)
	}
	if !foundNetHTTP {
		if deps.Module.Mod.Path == netHTTPPath {
			return mods, nil
		}
		// Add the net/http contrib module if not present because it's used by appsec.
		mods = append(mods, netHTTPPath)
	}
	return mods, nil
}

func pinMod(path string) error {
	importPath := fmt.Sprintf("%s@v2-dev", path)
	cmd := exec.Command("go", "get", "-u", importPath)
	fmt.Println(cmd.String())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func modTidy(goVersion string) error {
	majorVersion := strings.Join(strings.Split(goVersion, ".")[0:2], ".")
	cmd := exec.Command(fmt.Sprintf("go%s", goVersion), "mod", "tidy", "-go", majorVersion)
	fmt.Println(cmd.String())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func main() {
	p := pinning{}
	p.loadHeadCommit()
	p.pin("internal/apps/go.mod", "1.21.13")
	p.pin("internal/**/**/go.mod", "1.21.13")
	p.pin("contrib/**/**/go.mod", "1.21.13")
	p.pin("contrib/**/**/**/go.mod", "1.21.13")
}
