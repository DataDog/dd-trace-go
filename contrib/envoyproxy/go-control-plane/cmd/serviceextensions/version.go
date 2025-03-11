// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import "runtime/debug"

var version string

func init() {
	info, _ := debug.ReadBuildInfo()
	for _, dep := range info.Deps {
		if dep.Path == "github.com/DataDog/dd-trace-go/v2" {
			version = dep.Version
			break
		}
	}
}
