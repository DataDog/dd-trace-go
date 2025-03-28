// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

//go:build scripts
// +build scripts

package main

import (
	"fmt"

	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

func ghOutput(varName, v string) string {
	return fmt.Sprintf("::set-output name=%s::%s\n", varName, v)
}

func main() {
	noRC := fmt.Sprintf("v%d.%d.%d", version.Major, version.Minor, version.Patch)
	noPatch := fmt.Sprintf("v%d.%d.x", version.Major, version.Minor)
	nextMin := fmt.Sprintf("v%d.%d.%d", version.Major, version.Minor+1, version.Patch)
	nextPatch := fmt.Sprintf("v%d.%d.%d", version.Major, version.Minor, version.Patch+1)
	nextRC := fmt.Sprintf("v%d.%d.%d-rc.%d", version.Major, version.Minor, version.Patch, version.RC+1)
	fmt.Printf("The current version is %s (without rc suffix: %s)\n", version.Tag, noRC)
	fmt.Printf("The next minor version is %s\n", nextMin)
	fmt.Printf("The next patch version is %s\n", nextPatch)
	fmt.Printf("The next rc version is %s\n", nextRC)

	fmt.Printf(ghOutput("current", version.Tag))
	fmt.Printf(ghOutput("current_without_rc_suffix", noRC))
	fmt.Printf(ghOutput("current_without_patch", noPatch))
	fmt.Printf(ghOutput("next_minor", nextMin))
	fmt.Printf(ghOutput("next_patch", nextPatch))
	fmt.Printf(ghOutput("next_rc", nextRC))
}
