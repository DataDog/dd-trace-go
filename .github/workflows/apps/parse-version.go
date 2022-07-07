// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package main

import (
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

func main() {
	noRC := fmt.Sprintf("v%d.%d.%d", version.Major, version.Minor, version.Patch)
	nextMin := fmt.Sprintf("v%d.%d.%d", version.Major, version.Minor+1, version.Patch)
	nextPatch := fmt.Sprintf("v%d.%d.%d", version.Major, version.Minor, version.Patch+1)
	nextRC := fmt.Sprintf("v%d.%d.%d-rc.%d", version.Major, version.Minor, version.Patch, version.RC+1)
	fmt.Printf("The current version is %s (without rc suffix: %s)\n", version.Tag, noRC)
	fmt.Printf("The next minor version is %s\n", nextMin)
	fmt.Printf("The next patch version is %s\n", nextPatch)
	fmt.Printf("The next rc version is %s\n", nextRC)

	fmt.Printf("::set-output name=current::%s\n", version.Tag)
	fmt.Printf("::set-output name=current_without_rc_suffix::%s\n", noRC)
	fmt.Printf("::set-output name=current_without_patch::v%d.%d.x\n", version.Major, version.Minor)
	fmt.Printf("::set-output name=next_minor::%s\n", nextMin)
	fmt.Printf("::set-output name=next_patch::%s\n", nextPatch)
	fmt.Printf("::set-output name=next_rc::%s\n", nextRC)
}
