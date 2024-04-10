// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2check

import (
	"context"
	"fmt"
	"go/ast"

	"golang.org/x/tools/go/analysis"
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

func (c *nodeHandler) SetNode(node ast.Node) {
	c.node = node
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
	contextHandler
	nodeHandler
}

func (c V1ImportURL) Fixes() []analysis.SuggestedFix {
	return []analysis.SuggestedFix{
		{
			Message: "update import URL to v2",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     c.node.Pos(),
					End:     c.node.End(),
					NewText: []byte(`"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"`),
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
