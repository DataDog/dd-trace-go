// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSearchCommitsResponseMissingCommitsPreservesLocalOrder(t *testing.T) {
	response := newSearchCommitsResponse(
		[]string{"local-1", "remote-1", "local-2", "remote-2", "local-3"},
		[]string{"remote-2", "remote-1"},
		true,
	)

	assert.Equal(t, []string{"local-1", "local-2", "local-3"}, response.missingCommits())
}

func TestSearchCommitsResponseMissingCommitsKeepsMissingDuplicates(t *testing.T) {
	response := newSearchCommitsResponse(
		[]string{"missing-1", "remote-1", "missing-1", "missing-2"},
		[]string{"remote-1"},
		true,
	)

	assert.Equal(t, []string{"missing-1", "missing-1", "missing-2"}, response.missingCommits())
}

func TestSearchCommitsResponseMissingCommitsReturnsEmptyWhenAllLocalCommitsAreRemote(t *testing.T) {
	response := newSearchCommitsResponse(
		[]string{"remote-1", "remote-2"},
		[]string{"remote-2", "remote-1"},
		true,
	)

	assert.Empty(t, response.missingCommits())
}
