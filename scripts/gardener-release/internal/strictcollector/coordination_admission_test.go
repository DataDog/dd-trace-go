// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

func TestStateV3CompactCoordinationHistoryOwnsFixedMetadata(t *testing.T) {
	var history stateV3CompactCoordinationHistory
	if got, want := len(history.snapshots), gardenerrelease.MaxStateV3CoordinationHistoryCommits; got != want {
		t.Fatalf("compact coordination capacity=%d want=%d", got, want)
	}
	assertFixedCompactType(t, reflect.TypeFor[stateV3CompactCoordinationHistory]())
}

func TestStateV3CompactCoordinationAdmissionRequiresExactCheckpointBeforeBlobDispatch(t *testing.T) {
	policy := validStateV3Policy(t)
	policy.Coordination.CheckpointOID = compactAdmissionOID(700)
	policy.Coordination.MaxHistoryCommits = 2
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatal(err)
	}
	head := compactAdmissionOID(701)
	calls := 0
	child, operation := coordinationAdmissionChild(t, policy, roundTrip(func(request *http.Request) (*http.Response, error) {
		calls++
		if strings.Contains(request.URL.Path, "/git/blobs/") {
			t.Fatal("non-checkpoint coordination history dispatched a blob")
		}
		switch {
		case request.URL.Path == "/repos/"+repository+"/git/ref/heads/gardener-release-coordination":
			return response(compactAdmissionRefJSON(gardenerrelease.StateV3CoordinationRef, head)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+head:
			return response(spineRawJSON(head, compactAdmissionOID(1701), compactAdmissionOID(702))), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+head:
			return response(spineRESTJSON(head, compactAdmissionOID(1701), compactAdmissionOID(702))), nil
		case request.URL.Path == "/graphql":
			if strings.Contains(readRequestBody(t, request), head) {
				return response(spineGraphQLJSON(head)), nil
			}
			return response(spineGraphQLJSON(compactAdmissionOID(702))), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(1701):
			return response(compactAdmissionTreeJSON(compactAdmissionOID(1701), nil)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+compactAdmissionOID(702):
			return response(spineRawJSON(compactAdmissionOID(702), compactAdmissionOID(1702), compactAdmissionOID(703))), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+compactAdmissionOID(702):
			return response(spineRESTJSON(compactAdmissionOID(702), compactAdmissionOID(1702), compactAdmissionOID(703))), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(1702):
			return response(compactAdmissionTreeJSON(compactAdmissionOID(1702), nil)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+compactAdmissionOID(703):
			return response(spineRawJSON(compactAdmissionOID(703), compactAdmissionOID(1703), "")), nil
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
			return nil, nil
		}
	}))
	defer operation.close()
	result := child.collectStateV3CoordinationDocuments()
	if result.Diagnostic != DiagnosticRequiredEvidenceAbsent || calls != 10 {
		t.Fatalf("result=%#v calls=%d", result, calls)
	}
	compactAdmissionStoreIsZero(t, operation.store)
}

func TestStateV3CompactCoordinationAdmissionRejectsDirectoryOnlyCheckpointBeforeBlobDispatch(t *testing.T) {
	policy := validStateV3Policy(t)
	checkpoint, head := compactAdmissionOID(805), compactAdmissionOID(806)
	policy.Coordination.CheckpointOID, policy.Coordination.MaxHistoryCommits = checkpoint, 2
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatal(err)
	}
	child, operation := coordinationAdmissionChild(t, policy, roundTrip(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/git/blobs/") {
			t.Fatal("directory-only checkpoint dispatched a blob")
		}
		switch {
		case request.URL.Path == "/repos/"+repository+"/git/ref/heads/gardener-release-coordination":
			return response(compactAdmissionRefJSON(gardenerrelease.StateV3CoordinationRef, head)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+head:
			return response(spineRawJSON(head, compactAdmissionOID(1805), checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+head:
			return response(spineRESTJSON(head, compactAdmissionOID(1805), checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+checkpoint:
			return response(spineRawJSON(checkpoint, compactAdmissionOID(1806), "")), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+checkpoint:
			return response(spineRESTJSON(checkpoint, compactAdmissionOID(1806), "")), nil
		case request.URL.Path == "/graphql":
			if strings.Contains(readRequestBody(t, request), head) {
				return response(spineGraphQLJSON(head)), nil
			}
			return response(spineGraphQLJSON(checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(1805):
			return response(compactAdmissionTreeJSON(compactAdmissionOID(1805), nil)), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(1806):
			return response(`{"sha":"` + compactAdmissionOID(1806) + `","url":"u","truncated":false,"tree":[{"path":"unexpected","mode":"040000","type":"tree","sha":"` + compactAdmissionOID(1807) + `","url":"u"}]}`), nil
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
			return nil, nil
		}
	}))
	defer operation.close()
	if result := child.collectStateV3CoordinationDocuments(); result.Diagnostic != DiagnosticRequiredEvidenceAbsent {
		t.Fatalf("result=%#v", result)
	}
	compactAdmissionStoreIsZero(t, operation.store)
}

func TestStateV3CompactCoordinationAdmissionRetainsClaimFromRecursiveTree(t *testing.T) {
	policy := validStateV3Policy(t)
	checkpoint, head := compactAdmissionOID(810), compactAdmissionOID(811)
	policy.Coordination.CheckpointOID, policy.Coordination.MaxHistoryCommits = checkpoint, 2
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatal(err)
	}
	claimRaw := []byte(`{"attempt":0,"command":"","lane_expected_head_oid":"","lane_ref":"","phase":"","release_line":"v2.11","request_key":"","request_sha256":"","reservation_sha256":"","resolved_version":"","state":"","version_resolution_sha256":""}`)
	if !gardenerrelease.ValidateStateV3ReleaseLineClaimDocumentPath(claimRaw, "release-lines/2.11.json") {
		t.Fatalf("claim=%q", claimRaw)
	}
	claimOID := gitBlobOID(claimRaw)
	child, operation := coordinationAdmissionChild(t, policy, roundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/repos/"+repository+"/git/ref/heads/gardener-release-coordination":
			return response(compactAdmissionRefJSON(gardenerrelease.StateV3CoordinationRef, head)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+head:
			return response(spineRawJSON(head, compactAdmissionOID(1811), checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+head:
			return response(spineRESTJSON(head, compactAdmissionOID(1811), checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+checkpoint:
			return response(spineRawJSON(checkpoint, compactAdmissionOID(1812), "")), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+checkpoint:
			return response(spineRESTJSON(checkpoint, compactAdmissionOID(1812), "")), nil
		case request.URL.Path == "/graphql":
			if strings.Contains(readRequestBody(t, request), head) {
				return response(spineGraphQLJSON(head)), nil
			}
			return response(spineGraphQLJSON(checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(1811):
			return response(`{"sha":"` + compactAdmissionOID(1811) + `","url":"u","truncated":false,"tree":[{"path":"release-lines","mode":"040000","type":"tree","sha":"` + compactAdmissionOID(1813) + `","url":"u"},{"path":"release-lines/2.11.json","mode":"100644","type":"blob","sha":"` + claimOID + `","size":` + fmt.Sprint(len(claimRaw)) + `,"url":"u"}]} `), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(1812):
			return response(compactAdmissionTreeJSON(compactAdmissionOID(1812), nil)), nil
		case request.URL.Path == "/repos/"+repository+"/git/blobs/"+claimOID:
			return response(compactAdmissionBlobJSON(claimOID, claimRaw)), nil
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
			return nil, nil
		}
	}))
	defer operation.close()
	if result := child.collectStateV3CoordinationDocuments(); result.Diagnostic != DiagnosticOK {
		t.Fatalf("result=%#v", result)
	}
	if operation.store.blobCount != 1 || operation.store.bindingCount != 1 {
		t.Fatalf("claims were not retained: blobs=%d bindings=%d", operation.store.blobCount, operation.store.bindingCount)
	}
}

func TestStateV3CompactCoordinationAdmissionAcceptsEmptyCheckpointBoundHistory(t *testing.T) {
	policy := validStateV3Policy(t)
	checkpoint, head := compactAdmissionOID(800), compactAdmissionOID(801)
	policy.Coordination.CheckpointOID, policy.Coordination.MaxHistoryCommits = checkpoint, 2
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatal(err)
	}
	child, operation := coordinationAdmissionChild(t, policy, roundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/repos/"+repository+"/git/ref/heads/gardener-release-coordination":
			return response(compactAdmissionRefJSON(gardenerrelease.StateV3CoordinationRef, head)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+head:
			return response(spineRawJSON(head, compactAdmissionOID(1801), checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+head:
			return response(spineRESTJSON(head, compactAdmissionOID(1801), checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+checkpoint:
			return response(spineRawJSON(checkpoint, compactAdmissionOID(1802), "")), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+checkpoint:
			return response(spineRESTJSON(checkpoint, compactAdmissionOID(1802), "")), nil
		case request.URL.Path == "/graphql":
			if strings.Contains(readRequestBody(t, request), head) {
				return response(spineGraphQLJSON(head)), nil
			}
			return response(spineGraphQLJSON(checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(1801):
			return response(compactAdmissionTreeJSON(compactAdmissionOID(1801), nil)), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(1802):
			return response(compactAdmissionTreeJSON(compactAdmissionOID(1802), nil)), nil
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
			return nil, nil
		}
	}))
	defer operation.close()
	if result := child.collectStateV3CoordinationDocuments(); result.Diagnostic != DiagnosticOK {
		t.Fatalf("result=%#v", result)
	}
	compactAdmissionStoreIsZero(t, operation.store)
}

func readRequestBody(t *testing.T, request *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func coordinationAdmissionChild(t *testing.T, policy gardenerrelease.StateV3Policy, transport http.RoundTripper) (*stateV3AssemblyChild, *stateV3AssemblyOperation) {
	t.Helper()
	noCall := roundTrip(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected non-coordination request %s", request.URL.Path)
		return nil, nil
	})
	operation := &stateV3AssemblyOperation{minor: newTestSession(noCall, time.Now), patch: newTestSession(noCall, time.Now), coordination: newTestSession(transport, time.Now)}
	if result := operation.begin(context.Background(), time.Now().Add(time.Minute), policy); result.Diagnostic != DiagnosticOK {
		t.Fatalf("begin=%#v", result)
	}
	for _, role := range [...]stateV3AssemblyRole{stateV3AssemblyMinor, stateV3AssemblyPatch} {
		child, result := operation.beginChild(role)
		if result.Diagnostic != DiagnosticOK {
			t.Fatalf("begin %d: %#v", role, result)
		}
		if result := operation.finishChild(child); result.Diagnostic != DiagnosticOK {
			t.Fatalf("finish %d: %#v", role, result)
		}
	}
	child, result := operation.beginChild(stateV3AssemblyCoordination)
	if result.Diagnostic != DiagnosticOK {
		t.Fatalf("begin coordination: %#v", result)
	}
	return child, operation
}
