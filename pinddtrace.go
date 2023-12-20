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

	"golang.org/x/mod/modfile"
)

func main() {
	pin("internal/apps/go.mod", "1.21.3")
	pin("internal/**/**/go.mod", "1.19.9")
	pin("v2/contrib/**/**/go.mod", "1.19.9")
	pin("v2/contrib/**/**/**/go.mod", "1.19.9")
}

func pin(pattern, goVersion string) {
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
		fmt.Println(d)
		mods, err := loadDDTraceMods()
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
	}
}

func loadDDTraceMods() ([]string, error) {
	mod, err := os.Open("go.mod")
	if err != nil {
		return nil, err
	}
	defer mod.Close()
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(mod); err != nil {
		return nil, err
	}
	deps, err := modfile.Parse("go.mod", buf.Bytes(), nil)
	if err != nil {
		return nil, err
	}
	mods := make([]string, 0, len(deps.Replace))
	for _, dep := range deps.Replace {
		mods = append(mods, dep.Old.Path)
	}
	return mods, nil
}

func pinMod(path string) error {
	importPath := fmt.Sprintf("%s@v2-dev", path)
	cmd := exec.Command("go", "get", "-u", importPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func modTidy(goVersion string) error {
	cmd := exec.Command(fmt.Sprintf("go%s", goVersion), "mod", "tidy")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
