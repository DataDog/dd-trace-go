// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2check

import (
	"go/ast"
	"go/types"
	"os"
	"path"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

// newV1Usage detects the usage of any v1 function.
func newV1Usage() *knownChange {
	var d knownChange
	d.ctx = make(context)
	d.probes = []func(context, ast.Node, *analysis.Pass) bool{
		isFuncCall,
		hasPackagePrefix("gopkg.in/DataDog/dd-trace-go.v1/"),
	}
	d.message = func() string {
		fn, ok := d.ctx["fn"].(*types.Func)
		if !ok {
			return "unknown"
		}
		return fn.FullName()
	}
	return &d
}

func TestMain(m *testing.M) {
	knownChanges = []*knownChange{
		newV1Usage(),
	}

	os.Exit(m.Run())
}

func TestSimple(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Error(err)
	}
	_ = analysistest.Run(t, path.Join(cwd, "../_stage"), Analyzer)
}
