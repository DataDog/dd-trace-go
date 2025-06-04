// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2fix

import (
	"context"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path"
	"testing"

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

func (*V1Usage) SetNode(ast.Node) {}

func (c V1Usage) Fixes() []analysis.SuggestedFix {
	return nil
}

func (c V1Usage) Probes() []Probe {
	return []Probe{
		IsFuncCall,
		HasPackagePrefix("gopkg.in/DataDog/dd-trace-go.v1"),
	}
}

func (c V1Usage) String() string {
	fn, ok := c.ctx.Value(fnKey).(*types.Func)
	if !ok {
		return "unknown"
	}
	return fn.FullName()
}

func TestV1Usage(t *testing.T) {
	c := NewChecker(&V1Usage{
		ctx: context.Background(),
	})
	c.Run(testRunner(t, "v1usage"))
}

func TestV1ImportURL(t *testing.T) {
	c := NewChecker(&V1ImportURL{})
	c.Run(testRunner(t, "v1importurl"))
}

func TestDDTraceTypes(t *testing.T) {
	c := NewChecker(&DDTraceTypes{})
	c.Run(testRunner(t, "ddtracetypes"))
}

func TestWithServiceName(t *testing.T) {
	c := NewChecker(&WithServiceName{})
	c.Run(testRunner(t, "withservicename"))
}

func TestTraceIDString(t *testing.T) {
	c := NewChecker(&TraceIDString{})
	c.Run(testRunner(t, "traceidstring"))
}

func TestTracerStructs(t *testing.T) {
	c := NewChecker(&TracerStructs{})
	c.Run(testRunner(t, "tracerstructs"))
}

func TestWithDogstatsdAddr(t *testing.T) {
	c := NewChecker(&WithDogstatsdAddr{})
	c.Run(testRunner(t, "withdogstatsdaddr"))
}

func TestDeprecatedSamplingRules(t *testing.T) {
	c := NewChecker(&DeprecatedSamplingRules{})
	c.Run(testRunner(t, "samplingrules"))
}

func testRunner(t *testing.T, name string) func(*analysis.Analyzer) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Error(err)
		return nil
	}
	return func(a *analysis.Analyzer) {
		analysistest.RunWithSuggestedFixes(t, path.Join(cwd, "..", "_stage"), a, fmt.Sprintf("./%s", name))
	}
}
