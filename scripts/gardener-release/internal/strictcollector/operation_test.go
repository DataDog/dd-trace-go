// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

func TestStateV3LaneTerminationPrefixesAreFixedAndRawFree(t *testing.T) {
	assertFixedCompactType(t, reflect.TypeFor[stateV3LaneTerminationPrefixes]())
}

func TestStateV3AssemblyOperationAdmissionAndOrder(t *testing.T) {
	minor := newTestSession(roundTrip(func(*http.Request) (*http.Response, error) {
		return response(refJSON("refs/heads/gardener-release-state/minor")), nil
	}), time.Now)
	patch := newTestSession(roundTrip(func(*http.Request) (*http.Response, error) {
		return response(refJSON("refs/heads/gardener-release-state/patch")), nil
	}), time.Now)
	coordination := newTestSession(roundTrip(func(*http.Request) (*http.Response, error) {
		return response(refJSON("refs/heads/gardener-release-coordination")), nil
	}), time.Now)
	op := stateV3AssemblyOperation{minor: minor, patch: patch, coordination: coordination}
	if result := op.begin(context.Background(), time.Now().Add(time.Second), validStateV3Policy(t)); result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	if _, result := op.beginChild(stateV3AssemblyPatch); result.Diagnostic != DiagnosticProtocol {
		t.Fatal(result)
	}
	child, result := op.beginChild(stateV3AssemblyMinor)
	if result.Diagnostic != DiagnosticOK || child == nil {
		t.Fatal(result)
	}
	if _, result := minor.ReadPatchStateRef(context.Background(), time.Now().Add(time.Second)); result.Diagnostic != DiagnosticProtocol {
		t.Fatal("ordinary read spent an assembly reservation")
	}
	if _, result := op.beginChild(stateV3AssemblyPatch); result.Diagnostic != DiagnosticProtocol {
		t.Fatal(result)
	}
	if result := op.finishChild(child); result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	if _, result := op.beginChild(stateV3AssemblyPatch); result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	op.close()
	for _, session := range []*session{minor, patch, coordination} {
		if !session.closed || len(session.artifacts) != 0 {
			t.Fatal("operation left a child session live")
		}
	}
}

func TestStateV3AssemblyChildUsesOnlyRoleFixedRoot(t *testing.T) {
	cases := []struct {
		role stateV3AssemblyRole
		ref  string
	}{
		{stateV3AssemblyMinor, "refs/heads/gardener-release-state/minor"},
		{stateV3AssemblyPatch, "refs/heads/gardener-release-state/patch"},
		{stateV3AssemblyCoordination, "refs/heads/gardener-release-coordination"},
	}
	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			var got string
			newChild := func() *session {
				return newTestSession(roundTrip(func(req *http.Request) (*http.Response, error) {
					got = req.URL.Path
					return response(refJSON(tc.ref)), nil
				}), time.Now)
			}
			op := stateV3AssemblyOperation{minor: newChild(), patch: newChild(), coordination: newChild()}
			if result := op.begin(context.Background(), time.Now().Add(time.Second), validStateV3Policy(t)); result.Diagnostic != DiagnosticOK {
				t.Fatal(result)
			}
			var child *stateV3AssemblyChild
			for role := stateV3AssemblyMinor; role <= tc.role; role++ {
				var result Result
				child, result = op.beginChild(role)
				if result.Diagnostic != DiagnosticOK {
					t.Fatal(result)
				}
				if role != tc.role {
					if result := op.finishChild(child); result.Diagnostic != DiagnosticOK {
						t.Fatal(result)
					}
				}
			}
			if _, result := child.readRoot(); result.Diagnostic != DiagnosticOK {
				t.Fatal(result)
			}
			want := "/repos/" + repository + "/git/ref/" + strings.TrimPrefix(tc.ref, "refs/")
			if got != want {
				t.Fatalf("path=%q want %q", got, want)
			}
			op.close()
		})
	}
}

func TestStateV3AssemblyOperationRejectsInvalidBeforeDispatch(t *testing.T) {
	var calls atomic.Int32
	newChild := func() *session {
		return newTestSession(roundTrip(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, nil }), time.Now)
	}
	op := stateV3AssemblyOperation{minor: newChild(), patch: newChild(), coordination: newChild()}
	if result := op.begin(nil, time.Now().Add(time.Second), validStateV3Policy(t)); result.Diagnostic != DiagnosticDeadline {
		t.Fatal(result)
	}
	if calls.Load() != 0 {
		t.Fatal("preflight dispatched")
	}
}

func TestStateV3AssemblyOperationInvalidBeginDoesNotCloseActiveOperation(t *testing.T) {
	child := func() *session {
		return newTestSession(roundTrip(func(*http.Request) (*http.Response, error) {
			return response(refJSON("refs/heads/gardener-release-state/minor")), nil
		}), time.Now)
	}
	op := stateV3AssemblyOperation{minor: child(), patch: child(), coordination: child()}
	if result := op.begin(context.Background(), time.Now().Add(time.Second), validStateV3Policy(t)); result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	if result := op.begin(nil, time.Now().Add(time.Second), validStateV3Policy(t)); result.Diagnostic != DiagnosticDeadline {
		t.Fatal(result)
	}
	if !op.active || op.minor.closed || op.patch.closed || op.coordination.closed {
		t.Fatal("invalid contender closed active operation")
	}
	op.close()
}

type partialErrorReader struct{ read bool }

func (r *partialErrorReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, errors.New("partial read")
	}
	r.read = true
	copy(p, []byte("partial"))
	return len("partial"), errors.New("partial read")
}
func (r *partialErrorReader) Close() error { return nil }

func TestStateV3AssemblyChargesPartialReadError(t *testing.T) {
	minor := newTestSession(roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: &partialErrorReader{}}, nil
	}), time.Now)
	patch := newTestSession(roundTrip(func(*http.Request) (*http.Response, error) {
		return response(refJSON("refs/heads/gardener-release-state/patch")), nil
	}), time.Now)
	coordination := newTestSession(roundTrip(func(*http.Request) (*http.Response, error) {
		return response(refJSON("refs/heads/gardener-release-coordination")), nil
	}), time.Now)
	op := stateV3AssemblyOperation{minor: minor, patch: patch, coordination: coordination}
	if result := op.begin(context.Background(), time.Now().Add(10*time.Second), validStateV3Policy(t)); result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	child, result := op.beginChild(stateV3AssemblyMinor)
	if result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	before := op.quota.bytes
	if _, result := child.readRoot(); result.Diagnostic != DiagnosticTransport {
		t.Fatal(result)
	}
	if op.quota.bytes != before-maxAttempts*len("partial") {
		t.Fatal("partial response bytes were not charged")
	}
	op.close()
}

func TestStateV3AssemblyChargesOversizedResponse(t *testing.T) {
	closed := atomic.Bool{}
	minor := newTestSession(roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: closeRecorder{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBytes+1))), closed: &closed}}, nil
	}), time.Now)
	op := stateV3AssemblyOperation{minor: minor, patch: newTestSession(nil, time.Now), coordination: newTestSession(nil, time.Now)}
	if result := op.begin(context.Background(), time.Now().Add(time.Second), validStateV3Policy(t)); result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	child, result := op.beginChild(stateV3AssemblyMinor)
	if result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	before := op.quota.bytes
	h, result := child.readRoot()
	if result.Diagnostic != DiagnosticProtocol || h != (handle{}) || !closed.Load() {
		t.Fatalf("result=%#v closed=%v", result, closed.Load())
	}
	if op.quota.bytes != before-(maxResponseBytes+1) || minor.assemblyBytes != stateV3AssemblyChildResponseBytes-(maxResponseBytes+1) {
		t.Fatal("oversized body was not charged")
	}
	if len(minor.artifacts) != 0 {
		t.Fatal("oversized response issued artifact")
	}
	op.close()
}

type closeRecorder struct {
	io.ReadCloser
	closed *atomic.Bool
}

func (r closeRecorder) Close() error { r.closed.Store(true); return r.ReadCloser.Close() }

func TestStateV3AssemblyCloseCancelsActiveRead(t *testing.T) {
	started := make(chan struct{})
	minor := newTestSession(roundTrip(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	}), time.Now)
	op := stateV3AssemblyOperation{minor: minor, patch: newTestSession(nil, time.Now), coordination: newTestSession(nil, time.Now)}
	if result := op.begin(context.Background(), time.Now().Add(time.Second), validStateV3Policy(t)); result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	child, result := op.beginChild(stateV3AssemblyMinor)
	if result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	done := make(chan Result, 1)
	go func() { _, result := child.readRoot(); done <- result }()
	<-started
	op.close()
	if result := <-done; result.Diagnostic != DiagnosticDeadline {
		t.Fatal(result)
	}
	for _, s := range []*session{op.minor, op.patch, op.coordination} {
		if !s.closed || len(s.artifacts) != 0 {
			t.Fatal("operation cleanup failed")
		}
	}
}

func TestStateV3AssemblyDeadlineCancelsActiveRead(t *testing.T) {
	started := make(chan struct{})
	minor := newTestSession(roundTrip(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	}), time.Now)
	op := stateV3AssemblyOperation{minor: minor, patch: newTestSession(nil, time.Now), coordination: newTestSession(nil, time.Now)}
	// The test observes deadline propagation through a cancellation-respecting
	// transport. Leave scheduling headroom for race instrumentation; the
	// transport still blocks until the operation-owned deadline cancels it.
	if result := op.begin(context.Background(), time.Now().Add(250*time.Millisecond), validStateV3Policy(t)); result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	child, result := op.beginChild(stateV3AssemblyMinor)
	if result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	done := make(chan Result, 1)
	go func() { _, result := child.readRoot(); done <- result }()
	<-started
	if result := <-done; result.Diagnostic != DiagnosticDeadline {
		t.Fatal(result)
	}
	op.close()
	if !minor.closed || len(minor.artifacts) != 0 {
		t.Fatal("deadline cleanup failed")
	}
}

func TestStateV3AssemblyCollectsThreeSpinesUnderBoundPolicy(t *testing.T) {
	policy := validStateV3Policy(t)
	minorOID, patchOID, coordinationOID := compactAdmissionOID(20_001), compactAdmissionOID(20_002), compactAdmissionOID(20_003)
	policy.StateLanes.Minor.CheckpointOID = minorOID
	policy.StateLanes.Patch.CheckpointOID = patchOID
	policy.Coordination.CheckpointOID = coordinationOID
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatal(err)
	}
	var roots []string
	newChild := func(ref, oid, tree string) *session {
		return newTestSession(roundTrip(func(request *http.Request) (*http.Response, error) {
			switch {
			case request.URL.Path == "/repos/"+repository+"/git/ref/"+strings.TrimPrefix(ref, "refs/"):
				roots = append(roots, ref)
				return response(compactAdmissionRefJSON(ref, oid)), nil
			case request.URL.Path == "/repos/"+repository+"/git/commits/"+oid:
				return response(spineRawJSON(oid, tree, "")), nil
			case request.URL.Path == "/repos/"+repository+"/commits/"+oid:
				return response(spineRESTJSON(oid, tree, "")), nil
			case request.URL.Path == "/graphql":
				return response(spineGraphQLJSON(oid)), nil
			case request.URL.Path == "/repos/"+repository+"/git/trees/"+tree:
				return response(compactAdmissionTreeJSON(tree, nil)), nil
			default:
				t.Fatalf("unexpected request %s", request.URL.Path)
				return nil, nil
			}
		}), time.Now)
	}
	op := stateV3AssemblyOperation{
		minor:        newChild(gardenerrelease.StateV3MinorStateRef, minorOID, compactAdmissionOID(21_001)),
		patch:        newChild(gardenerrelease.StateV3PatchStateRef, patchOID, compactAdmissionOID(21_002)),
		coordination: newChild(gardenerrelease.StateV3CoordinationRef, coordinationOID, compactAdmissionOID(21_003)),
	}
	if result := op.begin(context.Background(), time.Now().Add(time.Minute), policy); result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	// The operation must retain the admitted deep copy, not caller-owned policy
	// fields supplied to begin.
	policy.StateLanes.Minor.CheckpointOID = compactAdmissionOID(22_001)
	policy.StateLanes.Patch.CheckpointOID = compactAdmissionOID(22_002)
	policy.Coordination.CheckpointOID = compactAdmissionOID(22_003)
	if result := op.collectThreeSpines(); result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	want := []string{gardenerrelease.StateV3MinorStateRef, gardenerrelease.StateV3PatchStateRef, gardenerrelease.StateV3CoordinationRef}
	if !reflect.DeepEqual(roots, want) {
		t.Fatalf("root order=%v want=%v", roots, want)
	}
	if op.active || op.policy != nil || op.store != nil || op.terminations != (stateV3LaneTerminationPrefixes{}) {
		t.Fatal("three-spine operation retained state after close")
	}
}

func TestStateV3AssemblyThreeSpinesStopsAfterEarlierFailure(t *testing.T) {
	policy := validStateV3Policy(t)
	var patchCalls, coordinationCalls atomic.Int32
	minor := newTestSession(roundTrip(func(*http.Request) (*http.Response, error) {
		return response("{}"), nil
	}), time.Now)
	patch := newTestSession(roundTrip(func(*http.Request) (*http.Response, error) {
		patchCalls.Add(1)
		return nil, nil
	}), time.Now)
	coordination := newTestSession(roundTrip(func(*http.Request) (*http.Response, error) {
		coordinationCalls.Add(1)
		return nil, nil
	}), time.Now)
	op := stateV3AssemblyOperation{minor: minor, patch: patch, coordination: coordination}
	if result := op.begin(context.Background(), time.Now().Add(time.Minute), policy); result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	if result := op.collectThreeSpines(); result.Diagnostic == DiagnosticOK {
		t.Fatal("invalid minor root succeeded")
	}
	if patchCalls.Load() != 0 || coordinationCalls.Load() != 0 {
		t.Fatalf("later sessions dispatched: patch=%d coordination=%d", patchCalls.Load(), coordinationCalls.Load())
	}
	for _, child := range []*session{minor, patch, coordination} {
		if !child.closed || len(child.artifacts) != 0 {
			t.Fatal("failed three-spine operation did not close child")
		}
	}
}

func TestStateV3AssemblyQuotaBoundaries(t *testing.T) {
	quota := stateV3AssemblyQuota{attempts: 1, bytes: 1}
	if !quota.chargeAttempt() || quota.chargeAttempt() || !quota.chargeBytes(1) || quota.chargeBytes(1) {
		t.Fatal("quota boundary accepted")
	}
	if stateV3AssemblyLogicalReads != 11764 || stateV3AssemblyAttempts != 47056 || stateV3AssemblyResponseBytes != 576<<20 || stateV3AssemblyChildResponseBytes != 192<<20 {
		t.Fatal("stale topology quota")
	}
}
