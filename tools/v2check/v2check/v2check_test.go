// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2check_test

import (
	"context"
	"go/types"
	"os"
	"path"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/tools/v2check/v2check"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

type V1Usage struct {
	ctx context.Context
}

func (c V1Usage) Context() context.Context {
	return c.ctx
}

func (c *V1Usage) SetContext(ctx context.Context) {
	c.ctx = ctx
}

func (c V1Usage) Probes() []v2check.Probe {
	return []v2check.Probe{
		v2check.IsFuncCall,
		v2check.HasPackagePrefix("gopkg.in/DataDog/dd-trace-go.v1/"),
	}
}

func (c V1Usage) String() string {
	fn, ok := c.ctx.Value("fn").(*types.Func)
	if !ok {
		return "unknown"
	}
	return fn.FullName()
}

func TestSimple(t *testing.T) {
	c := v2check.NewChecker(&V1Usage{
		ctx: context.Background(),
	})
	c.Run(testRunner(t))
}

func testRunner(t *testing.T) func(*analysis.Analyzer) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Error(err)
		return nil
	}
	return func(a *analysis.Analyzer) {
		analysistest.Run(t, path.Join(cwd, "..", "_stage"), a)
	}
}
