// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

type sameNameFixtureSuiteA struct{}
type sameNameFixtureSuiteB struct{}

// TestSharedName provides a same-named declaration without the unskippable tag.
func (sameNameFixtureSuiteA) TestSharedName() {
	_ = 1
	_ = 2
	_ = 3
}

// TestSharedName provides a same-named declaration with a declaration-level unskippable tag.
//
//dd:test.unskippable
func (sameNameFixtureSuiteB) TestSharedName() {
	_ = 1
	_ = 2
	_ = 3
	_ = 4
	_ = 5
}
