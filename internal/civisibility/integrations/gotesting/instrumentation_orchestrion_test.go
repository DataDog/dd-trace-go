// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProcessRetrySubtestModuleName(t *testing.T) {
	t.Run("callback module without parent metadata", func(t *testing.T) {
		assert.Equal(t, "helper/module", subtestModuleName("helper/module", nil))
	})

	t.Run("callback module without parent identity", func(t *testing.T) {
		assert.Equal(t, "helper/module", subtestModuleName("helper/module", &testExecutionMetadata{}))
	})

	t.Run("callback module with empty parent module", func(t *testing.T) {
		parent := &testExecutionMetadata{identity: newTestIdentity("", "parent_test.go", "TestParent")}
		assert.Equal(t, "helper/module", subtestModuleName("helper/module", parent))
	})

	t.Run("parent module", func(t *testing.T) {
		parent := &testExecutionMetadata{identity: newTestIdentity("consumer/module", "parent_test.go", "TestParent")}
		assert.Equal(t, "consumer/module", subtestModuleName("helper/module", parent))
	})
}
