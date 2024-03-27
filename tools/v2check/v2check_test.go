// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package main

import (
	"os"
	"path"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestSimple(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Error(err)
	}
	_ = analysistest.Run(t, path.Join(cwd, "_stage"), analyzer)
}
