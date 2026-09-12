// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReconciliationW06RecordedRetryEmitsExactOriginalInputs(t *testing.T) {
	policy := validPolicyJSON()
	requestRaw := validDispatchJSON(policy)
	request, err := DecodeDispatchJSON(requestRaw)
	if err != nil {
		t.Fatal(err)
	}
	operation := `{"schema_version":"1","phase":"signed","request_key":"` + request.RequestKey + `","request_sha256":"` + request.RequestSHA256 + `","repository_id":"123","repository_full_name":"DataDog/dd-trace-go","issue_number":"456","original_comment_id":"789","acknowledgement_comment_id":"790","command":"release:promote","requested_version":"v2.11.0","policy_revision":"` + request.Context.PolicyRevision + `"}`
	inspection, err := InspectReconciliation(requestRaw, []byte(operation))
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Category != ReconcileRecorded || inspection.Phase != "signed" || inspection.Inputs.Command != request.Command || inspection.Inputs.Version != request.Version {
		t.Fatalf("inspection = %#v", inspection)
	}
	var contextFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(inspection.Inputs.Context), &contextFields); err != nil {
		t.Fatal(err)
	}
	if len(contextFields) != 7 {
		t.Fatalf("context keys = %v", contextFields)
	}
	for _, forbidden := range []string{"actor", "ref", "sha", "force", "feedback_only"} {
		if _, exists := contextFields[forbidden]; exists {
			t.Fatalf("unexpected caller-selectable field %q", forbidden)
		}
	}
}

func TestReconciliationW06AcknowledgedNoRecordPreservesIDs(t *testing.T) {
	requestRaw := validDispatchJSON(validPolicyJSON())
	inspection, err := InspectReconciliation(requestRaw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Category != ReconcileAcknowledgedNoRecord || !strings.Contains(inspection.Inputs.Context, `"original_comment_id":"789"`) || !strings.Contains(inspection.Inputs.Context, `"acknowledgement_comment_id":"790"`) {
		t.Fatalf("inspection = %#v", inspection)
	}
}

func TestReconciliationRejectsMismatchedRecordAndStrictJSON(t *testing.T) {
	requestRaw := validDispatchJSON(validPolicyJSON())
	request, err := DecodeDispatchJSON(requestRaw)
	if err != nil {
		t.Fatal(err)
	}
	operation := `{"schema_version":"1","phase":"signed","request_key":"123:789","request_sha256":"` + request.RequestSHA256 + `","repository_id":"123","repository_full_name":"DataDog/dd-trace-go","issue_number":"456","original_comment_id":"789","acknowledgement_comment_id":"791","command":"release:promote","requested_version":"v2.11.0","policy_revision":"` + request.Context.PolicyRevision + `"}`
	if _, err := InspectReconciliation(requestRaw, []byte(operation)); ErrorCode(err) != "reconciliation_record_mismatch" {
		t.Fatalf("error = %q", ErrorCode(err))
	}
	if _, err := InspectReconciliation(append(requestRaw, []byte(` {}`)...), nil); err == nil {
		t.Fatal("trailing request JSON accepted")
	}
}
