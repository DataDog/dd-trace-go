// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package main

import (
	"fmt"
	"os"
	"regexp"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: go run .github/workflows/apps/update_version_file.go <new version>")
		return
	}
	r := regexp.MustCompile(`v\d+\.\d+\.\d+(-rc\.\d+)?$`)
	newVersion := []byte(os.Args[1])
	if !r.Match(newVersion) {
		panic(fmt.Errorf("%s does not match %s", newVersion, r.String()))
	}
	data, err := os.ReadFile("internal/version/version.go")
	if err != nil {
		panic(fmt.Errorf("Couldn't find version.go"))
	}
	if os.WriteFile("internal/version/version.go", regexp.MustCompile(`v\d+\.\d+\.\d+(-rc\.\d+)?`).ReplaceAll(data, newVersion), os.ModePerm) != nil {
		panic(fmt.Errorf("Couldn't write version.go"))
	}
}
