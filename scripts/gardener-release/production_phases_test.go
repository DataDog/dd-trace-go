// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestGATagGateTrustsBothImageWorkflowFiles(t *testing.T) {
	parent := []byte("approved parent workflow")
	child := []byte("approved child workflow")
	parentDigest := sha256.Sum256(parent)
	childDigest := sha256.Sum256(child)
	policy := ImageObservationPolicy{WorkflowID: "30", WorkflowPath: ImageWorkflowPath, WorkflowSHA256: hex.EncodeToString(parentDigest[:]), ChildWorkflowSHA256: hex.EncodeToString(childDigest[:])}
	sha := strings.Repeat("a", 40)
	for _, test := range []struct {
		name      string
		workflows map[string][]byte
		wantCode  string
	}{
		{name: "matching digests", workflows: map[string][]byte{ImageWorkflowPath: parent, ImageChildWorkflowPath: child}},
		{name: "parent mismatch", workflows: map[string][]byte{ImageWorkflowPath: []byte("changed parent"), ImageChildWorkflowPath: child}, wantCode: "image_workflow_code_mismatch"},
		{name: "child mismatch", workflows: map[string][]byte{ImageWorkflowPath: parent, ImageChildWorkflowPath: []byte("changed child")}, wantCode: "image_workflow_code_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifyTrustedImageWorkflows(context.Background(), &fakeChecksAPI{workflows: test.workflows}, policy, sha)
			if ErrorCode(err) != test.wantCode {
				t.Fatalf("error = %q, want %q", ErrorCode(err), test.wantCode)
			}
		})
	}
}
