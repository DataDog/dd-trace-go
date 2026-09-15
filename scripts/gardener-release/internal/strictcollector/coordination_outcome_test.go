// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

func TestStateV3CompactCoordinationAdmissionRetainsOutcomeTranscript(t *testing.T) {
	policy := validStateV3Policy(t)
	checkpoint, head := compactAdmissionOID(2200), compactAdmissionOID(2201)
	policy.Coordination.CheckpointOID, policy.Coordination.MaxHistoryCommits = checkpoint, 2
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatal(err)
	}
	raw := coordinationOutcomeRaw()
	if !gardenerrelease.ValidateStateV3CoordinationMutationOutcomeDocumentPath(raw, gardenerrelease.StateV3CoordinationMutationOutcomePath) {
		t.Fatalf("outcome transcript rejected: %s", raw)
	}
	oid := gitBlobOID(raw)
	child, operation := coordinationAdmissionChild(t, policy, roundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/repos/"+repository+"/git/ref/heads/gardener-release-coordination":
			return response(compactAdmissionRefJSON(gardenerrelease.StateV3CoordinationRef, head)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+head:
			return response(spineRawJSON(head, compactAdmissionOID(2202), checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+head:
			return response(spineRESTJSON(head, compactAdmissionOID(2202), checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+checkpoint:
			return response(spineRawJSON(checkpoint, compactAdmissionOID(2203), "")), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+checkpoint:
			return response(spineRESTJSON(checkpoint, compactAdmissionOID(2203), "")), nil
		case request.URL.Path == "/graphql":
			if strings.Contains(readRequestBody(t, request), head) {
				return response(spineGraphQLJSON(head)), nil
			}
			return response(spineGraphQLJSON(checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(2202):
			return response(`{"sha":"` + compactAdmissionOID(2202) + `","url":"u","truncated":false,"tree":[{"path":"mutation-outcome.json","mode":"100644","type":"blob","sha":"` + oid + `","size":` + fmt.Sprint(len(raw)) + `,"url":"u"}]}`), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(2203):
			return response(compactAdmissionTreeJSON(compactAdmissionOID(2203), nil)), nil
		case request.URL.Path == "/repos/"+repository+"/git/blobs/"+oid:
			return response(compactAdmissionBlobJSON(oid, raw)), nil
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
			return nil, nil
		}
	}))
	defer operation.close()
	if result := child.collectStateV3CoordinationDocuments(); result.Diagnostic != DiagnosticOK {
		t.Fatalf("result=%#v", result)
	}
	if operation.store.blobCount != 1 || operation.store.bindingCount != 1 || operation.store.blobs[0].kind != stateV3DocumentCoordinationOutcome {
		t.Fatalf("outcome not retained: blobs=%d bindings=%d kind=%d", operation.store.blobCount, operation.store.bindingCount, operation.store.blobs[0].kind)
	}
}

func TestStateV3CoordinationOutcomeTreeShapeIsBounded(t *testing.T) {
	oid := compactAdmissionOID(2300)
	entries := []wireTreeEntry{
		{Path: "release-lines/2.11.json", Mode: "100644", Type: "blob", SHA: oid},
		{Path: "release-lines/2.12.json", Mode: "100644", Type: "blob", SHA: compactAdmissionOID(2301)},
		{Path: gardenerrelease.StateV3CoordinationMutationOutcomePath, Mode: "100644", Type: "blob", SHA: compactAdmissionOID(2302)},
	}
	if found, ok := stateV3DiscoverDocumentEntries(stateV3AssemblyCoordination, entries); !ok || len(found) != gardenerrelease.MaxStateV3CoordinationReleaseProofs+1 {
		t.Fatalf("outcome shape rejected: entries=%d ok=%v", len(found), ok)
	}
	entries = append(entries, wireTreeEntry{Path: "mutation-arm.json", Mode: "100644", Type: "blob", SHA: compactAdmissionOID(2303)})
	if _, ok := stateV3DiscoverDocumentEntries(stateV3AssemblyCoordination, entries); ok {
		t.Fatal("arm and outcome coexistence accepted")
	}
}

func coordinationOutcomeRaw() []byte {
	return []byte(`{"arm_blob_oid":"` + compactAdmissionOID(2401) + `","arm_commit_oid":"` + compactAdmissionOID(2405) + `","arm_path":"mutation-arm.json","arm_sha256":"` + fmt.Sprintf("%064x", 2402) + `","arm_tree_oid":"` + compactAdmissionOID(2404) + `","claim_blob_oid":"` + compactAdmissionOID(2408) + `","claim_commit_oid":"` + compactAdmissionOID(2407) + `","claim_path":"release-lines/2.11.json","claim_sha256":"` + fmt.Sprintf("%064x", 2406) + `","claim_tree_oid":"` + compactAdmissionOID(2409) + `","expected_head_oid":"` + compactAdmissionOID(2405) + `","intended_claim_sha256":"` + fmt.Sprintf("%064x", 2406) + `","lane_ref":"` + gardenerrelease.StateV3PatchStateRef + `","observed_ref_oid":"` + compactAdmissionOID(2407) + `","operation":"claim_acquire","release_line":"v2.11","request_key":"request-1","request_sha256":"` + fmt.Sprintf("%064x", 2410) + `","resolved_version":"v2.11.1","response":{"attempts":1,"observation":"observed","oid":"` + compactAdmissionOID(2407) + `"},"schema_version":"1","state_ref":"` + gardenerrelease.StateV3CoordinationRef + `"}`)
}

func TestStateV3CompactCoordinationAdmissionRejectsLaneMismatchedOutcomeBeforeRetention(t *testing.T) {
	policy := validStateV3Policy(t)
	checkpoint, head := compactAdmissionOID(2500), compactAdmissionOID(2501)
	policy.Coordination.CheckpointOID, policy.Coordination.MaxHistoryCommits = checkpoint, 2
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatal(err)
	}
	raw := []byte(strings.Replace(string(coordinationOutcomeRaw()), gardenerrelease.StateV3PatchStateRef, gardenerrelease.StateV3MinorStateRef, 1))
	if gardenerrelease.ValidateStateV3CoordinationMutationOutcomeDocumentPath(raw, gardenerrelease.StateV3CoordinationMutationOutcomePath) {
		t.Fatal("lane-mismatched outcome passed document validation")
	}
	oid := gitBlobOID(raw)
	child, operation := coordinationAdmissionChild(t, policy, roundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/repos/"+repository+"/git/ref/heads/gardener-release-coordination":
			return response(compactAdmissionRefJSON(gardenerrelease.StateV3CoordinationRef, head)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+head:
			return response(spineRawJSON(head, compactAdmissionOID(2502), checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+head:
			return response(spineRESTJSON(head, compactAdmissionOID(2502), checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+checkpoint:
			return response(spineRawJSON(checkpoint, compactAdmissionOID(2503), "")), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+checkpoint:
			return response(spineRESTJSON(checkpoint, compactAdmissionOID(2503), "")), nil
		case request.URL.Path == "/graphql":
			if strings.Contains(readRequestBody(t, request), head) {
				return response(spineGraphQLJSON(head)), nil
			}
			return response(spineGraphQLJSON(checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(2502):
			return response(`{"sha":"` + compactAdmissionOID(2502) + `","url":"u","truncated":false,"tree":[{"path":"mutation-outcome.json","mode":"100644","type":"blob","sha":"` + oid + `","size":` + fmt.Sprint(len(raw)) + `,"url":"u"}]}`), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(2503):
			return response(compactAdmissionTreeJSON(compactAdmissionOID(2503), nil)), nil
		case request.URL.Path == "/repos/"+repository+"/git/blobs/"+oid:
			return response(compactAdmissionBlobJSON(oid, raw)), nil
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
			return nil, nil
		}
	}))
	defer operation.close()
	if result := child.collectStateV3CoordinationDocuments(); result.Diagnostic != DiagnosticResponseInvalid {
		t.Fatalf("result=%#v", result)
	}
	if operation.store.blobCount != 0 || operation.store.bindingCount != 0 {
		t.Fatalf("invalid outcome retained: blobs=%d bindings=%d", operation.store.blobCount, operation.store.bindingCount)
	}
}
