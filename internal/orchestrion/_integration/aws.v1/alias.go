// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package awsv1

import (
	"context"
	"testing"

	awssession "github.com/aws/aws-sdk-go/aws/session"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a session is traced once when the session
// package is imported under another name.
type TestCaseAliasedImport struct {
	counted
}

func (tc *TestCaseAliasedImport) Setup(context.Context, *testing.T) {
	tc.sess = awssession.Must(awssession.NewSession(tc.cfg))
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return expectedRootTraces("aws.dynamodb")
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return 3
}
