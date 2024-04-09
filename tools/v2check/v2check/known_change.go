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
// A predicate can store information in the context, which is available to the message function and
// to the following probes.
// It is possible to declare fixes that will be suggested to the user or applied automatically.
type KnownChange interface {
	fmt.Stringer

	// Context returns the context associated with the known change.
	Context() context.Context

	// Probes returns a list of probes that must be true to report the analyzed expression.
	Probes() []Probe

	// UpdateContext updates the context with the given value.
	UpdateContext(context.Context)
}

func eval(k KnownChange, n ast.Node, pass *analysis.Pass) bool {
	for _, p := range k.Probes() {
		ctx, ok := p(k.Context(), n, pass)
		if !ok {
			return false
		}

		k.UpdateContext(ctx)
	}
	return true
}
