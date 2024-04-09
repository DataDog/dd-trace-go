// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package v2check

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
)

type context map[string]any

// knownChange models code expressions that must be changed to migrate to v2.
// It is defined by a set of probes that must be true to report the analyzed expression.
// It also contains a message function that returns a string describing the change.
// The probes are evaluated in order, and the first one that returns false
// will cause the expression to be ignored.
// A predicate can store information in the context, which is available to the message function and
// to the following probes.
// It is possible to declare fixes that will be suggested to the user or applied automatically.
type knownChange struct {
	ctx     context
	probes  []func(context, ast.Node, *analysis.Pass) bool
	fixes   []analysis.SuggestedFix
	message func() string
}

func (c *knownChange) eval(n ast.Node, pass *analysis.Pass) bool {
	c.ctx = make(context)
	for _, p := range c.probes {
		if !p(c.ctx, n, pass) {
			return false
		}
	}
	return true
}
