// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2fix

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"golang.org/x/tools/go/analysis"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

// KnownChange models code expressions that must be changed to migrate to v2.
// It is defined by a set of probes that must be true to report the analyzed expression.
// It also contains a message function that returns a string describing the change.
// The probes are evaluated in order, and the first one that returns false
// will cause the expression to be ignored.
// A probe can store information in the context, which is available to the following ones.
// It is possible to declare fixes that will be suggested to the user or applied automatically.
type KnownChange interface {
	fmt.Stringer

	// Context returns the context associated with the known change.
	Context() context.Context

	// Fixes returns a list of fixes that can be applied to the analyzed expression.
	Fixes() []analysis.SuggestedFix

	// Probes returns a list of probes that must be true to report the analyzed expression.
	Probes() []Probe

	// SetContext updates the context with the given value.
	SetContext(context.Context)

	// SetNode updates the node with the given value.
	SetNode(ast.Node)
}

type contextHandler struct {
	ctx context.Context
}

func (c contextHandler) Context() context.Context {
	if c.ctx == nil {
		c.ctx = context.Background()
	}
	return c.ctx
}

func (c *contextHandler) SetContext(ctx context.Context) {
	c.ctx = ctx
}

type nodeHandler struct {
	node ast.Node
}

func (n *nodeHandler) SetNode(node ast.Node) {
	n.node = node
}

type defaultKnownChange struct {
	contextHandler
	nodeHandler
}

func (d defaultKnownChange) End() token.Pos {
	end, ok := d.ctx.Value(endKey).(token.Pos)
	if ok {
		return end
	}
	if d.node == nil {
		return token.NoPos
	}
	return d.node.End()
}

func (d defaultKnownChange) Pos() token.Pos {
	pos, ok := d.ctx.Value(posKey).(token.Pos)
	if ok {
		return pos
	}
	if d.node == nil {
		return token.NoPos
	}
	return d.node.Pos()
}

func eval(k KnownChange, n ast.Node, pass *analysis.Pass) bool {
	for _, p := range k.Probes() {
		ctx, ok := p(k.Context(), n, pass)
		if !ok {
			return false
		}
		k.SetContext(ctx)
	}
	k.SetNode(n)
	return true
}

type V1ImportURL struct {
	defaultKnownChange
}

func (c V1ImportURL) Fixes() []analysis.SuggestedFix {
	path := c.ctx.Value(pkgPathKey).(string)
	if path == "" {
		return nil
	}
	path = strings.Replace(path, "gopkg.in/DataDog/dd-trace-go.v1", "github.com/DataDog/dd-trace-go/v2", 1)
	return []analysis.SuggestedFix{
		{
			Message: "update import URL to v2",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: []byte(fmt.Sprintf("%q", path)),
				},
			},
		},
	}
}

func (V1ImportURL) Probes() []Probe {
	return []Probe{
		IsImport,
		HasPackagePrefix("gopkg.in/DataDog/dd-trace-go.v1/"),
	}
}

func (V1ImportURL) String() string {
	return "import URL needs to be updated"
}

type DDTraceTypes struct {
	defaultKnownChange
}

func (c DDTraceTypes) Fixes() []analysis.SuggestedFix {
	typ, ok := c.ctx.Value(declaredTypeKey).(*types.Named)
	if !ok {
		return nil
	}
	newText := fmt.Sprintf("tracer.%s", typ.Obj().Name())
	return []analysis.SuggestedFix{
		{
			Message: "the declared type is in the ddtrace/tracer package now",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: []byte(newText),
				},
			},
		},
	}
}

func (DDTraceTypes) Probes() []Probe {
	return []Probe{
		Or(
			// TODO: add this in a types registration function used to
			// filter by, reduce iterations, and drop some probes.
			Is[*ast.ValueSpec],
			Is[*ast.Field],
		),
		ImportedFrom("gopkg.in/DataDog/dd-trace-go.v1"),
		Not(DeclaresType[ddtrace.SpanContext]()),
	}
}

func (DDTraceTypes) String() string {
	return "the declared type is in the ddtrace/tracer package now"
}

type TracerStructs struct {
	defaultKnownChange
}

func (c TracerStructs) Fixes() []analysis.SuggestedFix {
	typ, ok := c.ctx.Value(declaredTypeKey).(*types.Named)
	if !ok {
		return nil
	}
	typeDecl := fmt.Sprintf("%s.%s", typ.Obj().Pkg().Name(), typ.Obj().Name())
	return []analysis.SuggestedFix{
		{
			Message: "the declared type is now a struct, you need to use a pointer",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: []byte(fmt.Sprintf("*%s", typeDecl)),
				},
			},
		},
	}
}

func (TracerStructs) Probes() []Probe {
	return []Probe{
		Or(
			Is[*ast.ValueSpec],
			Is[*ast.Field],
		),
		Or(
			DeclaresType[tracer.Span](),
			DeclaresType[tracer.SpanContext](),
		),
	}
}

func (TracerStructs) String() string {
	return "the declared type is now a struct, you need to use a pointer"
}

type WithServiceName struct {
	defaultKnownChange
}

func (c WithServiceName) Fixes() []analysis.SuggestedFix {
	args, ok := c.ctx.Value(argsKey).([]string)
	if !ok || args == nil {
		return nil
	}

	return []analysis.SuggestedFix{
		{
			Message: "use WithService()",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: []byte(fmt.Sprintf("WithService(%s)", strings.Join(args, ", "))),
				},
			},
		},
	}
}

func (c WithServiceName) Probes() []Probe {
	return []Probe{
		IsFuncCall,
		WithFunctionName("WithServiceName"),
	}
}

func (c WithServiceName) String() string {
	return "the function WithServiceName is no longer supported. Use WithService instead."
}

type TraceIDString struct {
	defaultKnownChange
}

func (c TraceIDString) Fixes() []analysis.SuggestedFix {
	fn, ok := c.ctx.Value(fnKey).(func())
	if !ok || fn == nil {
		return nil
	}

	return []analysis.SuggestedFix{
		{
			Message: "use TraceIDLower()",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: []byte("TraceIDLower()"),
				},
			},
		},
	}
}

func (c TraceIDString) Probes() []Probe {
	return []Probe{
		IsFuncCall,
		WithFunctionName("TraceID"),
	}
}

func (c TraceIDString) String() string {
	return "trace IDs are now represented as strings, please use TraceIDLower to keep using 64-bits IDs, although it's recommended to switch to 128-bits with TraceID"
}

type WithDogstatsdAddr struct {
	defaultKnownChange
}

func (c WithDogstatsdAddr) Fixes() []analysis.SuggestedFix {
	args, ok := c.ctx.Value(argsKey).([]string)
	if !ok || args == nil {
		return nil
	}

	return []analysis.SuggestedFix{
		{
			Message: "use WithDogstatsdAddress()",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.Pos(),
					End:     c.End(),
					NewText: []byte(fmt.Sprintf("WithDogstatsdAddress(%s)", strings.Join(args, ", "))),
				},
			},
		},
	}
}

func (c WithDogstatsdAddr) Probes() []Probe {
	return []Probe{
		IsFuncCall,
		WithFunctionName("WithDogstatsdAddress"),
	}
}

func (c WithDogstatsdAddr) String() string {
	return "the function WithDogstatsdAddress is no longer supported. Use WithDogstatsdAddr instead."
}
