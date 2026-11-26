// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package graphgophers

import (
	"context"
	"testing"

	"github.com/graph-gophers/graphql-go"
)

// packageLevelSchema is parsed while the package is initialized.
var packageLevelSchema = graphql.MustParseSchema(schema, new(resolver))

// TestCasePackageLevelSchema checks that a schema parsed in a package-level
// variable is traced once.
type TestCasePackageLevelSchema struct {
	TestCase
}

func (tc *TestCasePackageLevelSchema) Setup(_ context.Context, t *testing.T) {
	tc.serve(t, packageLevelSchema)
}

func (*TestCasePackageLevelSchema) ExpectedSpanCount() int {
	return spansPerRequest
}
