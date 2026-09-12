// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package readcollector

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

func TestCollectStateV3SpineRejectsDeadlineBeforeTransport(t *testing.T) {
	calls := 0
	s := newSession(roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, nil
	}), time.Now)
	_, result := s.collectStateV3Spine(context.Background(), time.Now(), gardenerrelease.StateV3Policy{}, stateV3MinorSpine)
	if result.Diagnostic != DiagnosticDeadline || calls != 0 {
		t.Fatalf("result=%#v calls=%d", result, calls)
	}
	assertStateV3SpineSessionClosed(t, s)
}

func TestCollectStateV3SpineClosesSeededSessionOnPreflightFailure(t *testing.T) {
	s := newSession(roundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/repos/DataDog/dd-trace-go/git/ref/heads/gardener-release-state/minor" {
			t.Fatalf("unexpected seed request %s", request.URL.Path)
		}
		return response(refJSON(gardenerrelease.StateV3MinorStateRef)), nil
	}), time.Now)
	if _, result := s.ReadMinorStateRef(context.Background(), time.Now().Add(time.Second)); result.Diagnostic != DiagnosticOK {
		t.Fatalf("seed artifact: %#v", result)
	}
	if len(s.artifacts) == 0 || s.retainedBytes == 0 {
		t.Fatal("seed artifact was not retained")
	}
	_, result := s.collectStateV3Spine(context.Background(), time.Now(), gardenerrelease.StateV3Policy{}, stateV3MinorSpine)
	if result.Diagnostic != DiagnosticDeadline {
		t.Fatalf("result=%#v", result)
	}
	assertStateV3SpineSessionClosed(t, s)
}

func TestCollectStateV3SpineRejectsInvalidPolicyBeforeTransport(t *testing.T) {
	calls := 0
	s := newSession(roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, nil
	}), time.Now)
	_, result := s.collectStateV3Spine(context.Background(), time.Now().Add(time.Second), gardenerrelease.StateV3Policy{}, stateV3MinorSpine)
	if result.Diagnostic != DiagnosticProtocol || calls != 0 {
		t.Fatalf("result=%#v calls=%d", result, calls)
	}
	assertStateV3SpineSessionClosed(t, s)
}

func TestCollectStateV3SpineRejectsMaximumValidPolicyBeforeTransport(t *testing.T) {
	calls := 0
	s := newSession(roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, nil
	}), time.Now)
	policy := validStateV3Policy(t)
	policy.StateLanes.Minor.MaxHistoryCommits = gardenerrelease.MaxStateV3HistoryCommits
	_, result := s.collectStateV3Spine(context.Background(), time.Now().Add(time.Second), policy, stateV3MinorSpine)
	if result.Diagnostic != DiagnosticProtocol || calls != 0 {
		t.Fatalf("result=%#v calls=%d", result, calls)
	}
	assertStateV3SpineSessionClosed(t, s)
}

func TestCollectStateV3SpineRejectsUnknownRootBeforeTransport(t *testing.T) {
	calls := 0
	s := newSession(roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, nil
	}), time.Now)
	_, result := s.collectStateV3Spine(context.Background(), time.Now().Add(time.Second), gardenerrelease.StateV3Policy{}, stateV3SpineRoot(99))
	if result.Diagnostic != DiagnosticProtocol || calls != 0 {
		t.Fatalf("result=%#v calls=%d", result, calls)
	}
	assertStateV3SpineSessionClosed(t, s)
}

func TestStateV3SpinePolicyUsesOnlyFixedRoots(t *testing.T) {
	policy := gardenerrelease.StateV3Policy{
		StateLanes: gardenerrelease.StateV3LanePolicies{
			Minor: gardenerrelease.StateV3LanePolicy{StateRef: gardenerrelease.StateV3MinorStateRef},
			Patch: gardenerrelease.StateV3LanePolicy{StateRef: gardenerrelease.StateV3PatchStateRef},
		},
		Coordination: gardenerrelease.StateV3CoordinationPolicy{StateRef: gardenerrelease.StateV3CoordinationRef},
	}
	for root, want := range map[stateV3SpineRoot]string{
		stateV3MinorSpine:        gardenerrelease.StateV3MinorStateRef,
		stateV3PatchSpine:        gardenerrelease.StateV3PatchStateRef,
		stateV3CoordinationSpine: gardenerrelease.StateV3CoordinationRef,
	} {
		got, _, _, ok := stateV3SpinePolicy(policy, root)
		if !ok || got != want {
			t.Fatalf("root=%d got=%q ok=%t want=%q", root, got, ok, want)
		}
	}
}

func TestCollectStateV3SpineCollectsFixedTwoNodeChainAndClosesSession(t *testing.T) {
	policy := validStateV3Policy(t)
	policy.StateLanes.Minor.CheckpointOID = wireParentOID
	policy.Coordination.CheckpointOID = "dddddddddddddddddddddddddddddddddddddddd"
	policy.StateLanes.Minor.MaxHistoryCommits = 4*gardenerrelease.MaxStateV3Tags + gardenerrelease.StateV3PrepareHistoryOverhead
	refBacking := strings.Repeat("x", 1<<20) + gardenerrelease.StateV3MinorStateRef
	checkpointBacking := strings.Repeat("y", 1<<20) + wireParentOID
	policy.StateLanes.Minor.StateRef = refBacking[len(refBacking)-len(gardenerrelease.StateV3MinorStateRef):]
	policy.StateLanes.Minor.CheckpointOID = checkpointBacking[len(checkpointBacking)-len(wireParentOID):]
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatalf("test policy: %v", err)
	}
	if raw, rawOK := decodeRawCommit([]byte(spineRawJSON(wireOID, wireTreeOID, wireParentOID)), wireOID); !rawOK {
		t.Fatal("raw fixture invalid")
	} else if rest, restOK := decodeRESTCommit([]byte(spineRESTJSON(wireOID, wireTreeOID, wireParentOID)), wireOID); !restOK {
		t.Fatal("REST fixture invalid")
	} else if gql, gqlOK := decodeGQLCommit([]byte(spineGraphQLJSON(wireOID)), wireOID); !gqlOK {
		t.Fatal("GraphQL fixture invalid")
	} else if tree, treeOK := decodeTree([]byte(spineTreeJSON(wireTreeOID, wireOID)), wireTreeOID); !treeOK {
		t.Fatal("tree fixture invalid")
	} else if _, ok := stateV3SnapshotEvidence(raw, rest, gql, tree, policy, false, &stateV3SpineBudget{}); !ok {
		roles, _ := stateV3CommitRoles(raw, rest, gql)
		t.Fatalf("fixture evidence mismatch: %#v", roles)
	}
	calls := 0
	graphQLCalls := 0
	s := newSession(roundTrip(func(request *http.Request) (*http.Response, error) {
		calls++
		switch {
		case request.URL.Path == "/repos/DataDog/dd-trace-go/git/ref/heads/gardener-release-state/minor":
			return response(refJSON(gardenerrelease.StateV3MinorStateRef)), nil
		case request.URL.Path == "/repos/DataDog/dd-trace-go/git/commits/"+wireOID:
			return response(spineRawJSON(wireOID, wireTreeOID, wireParentOID)), nil
		case request.URL.Path == "/repos/DataDog/dd-trace-go/commits/"+wireOID:
			return response(spineRESTJSON(wireOID, wireTreeOID, wireParentOID)), nil
		case request.URL.Path == "/graphql":
			graphQLCalls++
			if graphQLCalls == 1 {
				return response(spineGraphQLJSON(wireOID)), nil
			}
			return response(spineGraphQLJSON(wireParentOID)), nil
		case request.URL.Path == "/repos/DataDog/dd-trace-go/git/trees/"+wireTreeOID:
			return response(spineTreeJSON(wireTreeOID, wireOID)), nil
		case request.URL.Path == "/repos/DataDog/dd-trace-go/git/commits/"+wireParentOID:
			return response(spineRawJSON(wireParentOID, "dddddddddddddddddddddddddddddddddddddddd", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")), nil
		case request.URL.Path == "/repos/DataDog/dd-trace-go/commits/"+wireParentOID:
			return response(spineRESTJSON(wireParentOID, "dddddddddddddddddddddddddddddddddddddddd", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")), nil
		case request.URL.Path == "/repos/DataDog/dd-trace-go/git/trees/dddddddddddddddddddddddddddddddddddddddd":
			return response(spineTreeJSON("dddddddddddddddddddddddddddddddddddddddd", wireTreeOID)), nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
			return nil, nil
		}
	}), time.Now)
	deadline := time.Now().Add(time.Second)
	for range 3 {
		handle, result := s.ReadMinorStateRef(context.Background(), deadline)
		if result.Diagnostic != DiagnosticOK {
			t.Fatalf("seed result=%#v", result)
		}
		s.Release(handle)
	}
	spine, result := s.collectStateV3Spine(context.Background(), deadline, policy, stateV3MinorSpine)
	if result.Diagnostic != DiagnosticOK || calls != 12 {
		t.Fatalf("result=%#v calls=%d", result, calls)
	}
	if spine.stateRef != gardenerrelease.StateV3MinorStateRef || len(spine.snapshots) != 2 || spine.snapshots[0].Commit.ParentOID != wireParentOID || spine.snapshots[1].Commit.ParentOID != "" || len(spine.snapshots[0].Commit.ChangedPaths) != 1 {
		t.Fatalf("unexpected spine: %#v", spine)
	}
	if unsafe.StringData(spine.stateRef) == unsafe.StringData(policy.StateLanes.Minor.StateRef) || unsafe.StringData(spine.checkpointOID) == unsafe.StringData(policy.StateLanes.Minor.CheckpointOID) {
		t.Fatal("collected spine retained oversized policy backing storage")
	}
	if !s.closed || len(s.artifacts) != 0 || s.retainedBytes != 0 {
		t.Fatal("collection retained session artifacts")
	}
}

func TestCollectStateV3SpineUsesEachFixedRoot(t *testing.T) {
	for root, expectedRef := range map[stateV3SpineRoot]string{
		stateV3MinorSpine:        gardenerrelease.StateV3MinorStateRef,
		stateV3PatchSpine:        gardenerrelease.StateV3PatchStateRef,
		stateV3CoordinationSpine: gardenerrelease.StateV3CoordinationRef,
	} {
		t.Run(expectedRef, func(t *testing.T) {
			policy := validStateV3Policy(t)
			calls := 0
			s := newSession(roundTrip(func(request *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					if request.URL.Path != "/repos/DataDog/dd-trace-go/git/ref/"+strings.TrimPrefix(expectedRef, "refs/") {
						t.Fatalf("root=%d path=%q", root, request.URL.Path)
					}
					return response(refJSON(expectedRef)), nil
				}
				return response(`{"sha":null}`), nil
			}), time.Now)
			_, result := s.collectStateV3Spine(context.Background(), time.Now().Add(time.Second), policy, root)
			if result.Diagnostic != DiagnosticResponseInvalid || calls != 2 || !s.closed || len(s.artifacts) != 0 {
				t.Fatalf("result=%#v calls=%d closed=%t artifacts=%d", result, calls, s.closed, len(s.artifacts))
			}
		})
	}
}

func TestCollectStateV3SpineClearsArtifactsAfterMidSpineFailure(t *testing.T) {
	policy := validStateV3Policy(t)
	calls := 0
	s := newSession(roundTrip(func(request *http.Request) (*http.Response, error) {
		calls++
		if strings.Contains(request.URL.Path, "/git/ref/") {
			return response(refJSON(gardenerrelease.StateV3MinorStateRef)), nil
		}
		return response(`{"sha":null}`), nil
	}), time.Now)
	_, result := s.collectStateV3Spine(context.Background(), time.Now().Add(time.Second), policy, stateV3MinorSpine)
	if result.Diagnostic != DiagnosticResponseInvalid || calls != 2 {
		t.Fatalf("result=%#v calls=%d", result, calls)
	}
	if !s.closed || len(s.artifacts) != 0 || s.retainedBytes != 0 {
		t.Fatal("failed collection retained session artifacts")
	}
}

func TestStateV3SpineReadBudgetReservesFixedRootRead(t *testing.T) {
	maximum, ok := stateV3SpineReadBudget((maxReads - 1) / 4)
	if !ok || maximum != maxReads-3 {
		t.Fatalf("maximum single-spine history budget = %d, ok=%t", maximum, ok)
	}
	if _, ok := stateV3SpineReadBudget(maxReads / 4); ok {
		t.Fatal("history needing root read beyond session limit accepted")
	}
}

func TestCollectStateV3SpineRejectsPreConsumedReadCapacityBeforeTransport(t *testing.T) {
	var calls atomic.Int32
	s := newSession(roundTrip(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(refJSON(gardenerrelease.StateV3MinorStateRef)), nil
	}), time.Now)
	deadline := time.Now().Add(time.Second)
	for range 4 {
		handle, result := s.ReadMinorStateRef(context.Background(), deadline)
		if result.Diagnostic != DiagnosticOK {
			t.Fatalf("seed result=%#v", result)
		}
		s.Release(handle)
	}
	policy := validStateV3Policy(t)
	policy.StateLanes.Minor.MaxHistoryCommits = (maxReads - 1) / 4
	_, result := s.collectStateV3Spine(context.Background(), deadline, policy, stateV3MinorSpine)
	if result.Diagnostic != DiagnosticProtocol || calls.Load() != 4 {
		t.Fatalf("result=%#v calls=%d", result, calls.Load())
	}
	assertStateV3SpineSessionClosed(t, s)
	if s.reservedReads != 0 || s.spineLease != nil {
		t.Fatal("failed preflight retained spine reservation")
	}
}

func TestStateV3SpineReservationCannotBeSpentByOrdinaryRead(t *testing.T) {
	var calls atomic.Int32
	s := newSession(roundTrip(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(refJSON(gardenerrelease.StateV3MinorStateRef)), nil
	}), time.Now)
	lease, reserved := s.reserveStateV3SpineReads(2)
	if !reserved {
		t.Fatal("reserve spine reads")
	}
	forgedContext := context.WithValue(context.Background(), "state-v3-spine-lease", lease)
	if _, result := s.ReadMinorStateRef(forgedContext, time.Now().Add(time.Second)); result.Diagnostic != DiagnosticProtocol {
		t.Fatalf("ordinary read result=%#v", result)
	}
	if calls.Load() != 0 || s.reservedReads != 2 || lease.remaining != 2 {
		t.Fatalf("ordinary read spent reservation: calls=%d reserved=%d remaining=%d", calls.Load(), s.reservedReads, lease.remaining)
	}
	s.closeStateV3Spine(lease)
	assertStateV3SpineSessionClosed(t, s)
}

func TestCollectStateV3SpineAdmitsExactRemainingCapacity(t *testing.T) {
	var calls atomic.Int32
	s := newSession(roundTrip(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if strings.Contains(request.URL.Path, "/git/ref/") {
			return response(refJSON(gardenerrelease.StateV3MinorStateRef)), nil
		}
		return response(`{"sha":null}`), nil
	}), time.Now)
	deadline := time.Now().Add(time.Second)
	for range 3 {
		handle, result := s.ReadMinorStateRef(context.Background(), deadline)
		if result.Diagnostic != DiagnosticOK {
			t.Fatalf("seed result=%#v", result)
		}
		s.Release(handle)
	}
	policy := validStateV3Policy(t)
	policy.StateLanes.Minor.MaxHistoryCommits = (maxReads - 1) / 4
	_, result := s.collectStateV3Spine(context.Background(), deadline, policy, stateV3MinorSpine)
	if result.Diagnostic != DiagnosticResponseInvalid || calls.Load() != 5 {
		t.Fatalf("result=%#v calls=%d", result, calls.Load())
	}
	assertStateV3SpineSessionClosed(t, s)
}

func TestStateV3SpineReservationExcludesParallelCollection(t *testing.T) {
	policy := validStateV3Policy(t)
	rootStarted := make(chan struct{})
	unblockRoot := make(chan struct{})
	var calls atomic.Int32
	s := newSession(roundTrip(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if strings.Contains(request.URL.Path, "/git/ref/") {
			select {
			case <-rootStarted:
			default:
				close(rootStarted)
			}
			<-unblockRoot
			return response(refJSON(gardenerrelease.StateV3MinorStateRef)), nil
		}
		return response(`{"sha":null}`), nil
	}), time.Now)
	deadline := time.Now().Add(time.Second)
	var first stateV3Spine
	var firstResult Result
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		first, firstResult = s.collectStateV3Spine(context.Background(), deadline, policy, stateV3MinorSpine)
	}()
	<-rootStarted
	if _, normalResult := s.ReadMinorStateRef(context.Background(), deadline); normalResult.Diagnostic != DiagnosticProtocol || calls.Load() != 1 {
		t.Fatalf("normal=%#v calls=%d", normalResult, calls.Load())
	}
	second, secondResult := s.collectStateV3Spine(context.Background(), deadline, policy, stateV3MinorSpine)
	if secondResult.Diagnostic != DiagnosticProtocol || len(second.snapshots) != 0 || calls.Load() != 1 {
		t.Fatalf("second=%#v result=%#v calls=%d", second, secondResult, calls.Load())
	}
	close(unblockRoot)
	wait.Wait()
	if firstResult.Diagnostic != DiagnosticResponseInvalid || len(first.snapshots) != 0 || calls.Load() != 2 {
		t.Fatalf("first=%#v result=%#v calls=%d", first, firstResult, calls.Load())
	}
	assertStateV3SpineSessionClosed(t, s)
	if s.reservedReads != 0 || s.spineLease != nil || s.reads > maxReads {
		t.Fatalf("reservation state reads=%d reserved=%d lease=%p", s.reads, s.reservedReads, s.spineLease)
	}
}

func TestStateV3SnapshotEvidenceBoundsRetainedTreeEvidence(t *testing.T) {
	raw, rest, graphQL, tree, policy := validSpineEvidence()
	tree.Tree = make([]wireTreeEntry, 10_000)
	for index := range tree.Tree {
		tree.Tree[index] = wireTreeEntry{Path: fmt.Sprintf("control/%05d.json", index), Mode: "100644", Type: "blob", SHA: wireOID, Size: pointer(int64(1))}
	}
	if _, ok := stateV3SnapshotEvidence(raw, rest, graphQL, tree, policy, false, &stateV3SpineBudget{}); ok {
		t.Fatal("tree projection beyond assembly-local entry budget accepted")
	}

	budget := stateV3SpineBudget{}
	if !budget.reserve(maxStateV3SpineEntries, maxStateV3SpineBytes) || budget.reserve(1, 1) {
		t.Fatal("aggregate snapshot budget did not fail closed")
	}
}

func TestStateV3SnapshotEvidenceProjectsExactRegularLeafCapacity(t *testing.T) {
	raw, rest, graphQL, _, policy := validSpineEvidence()
	var document strings.Builder
	document.WriteString(`{"sha":"` + wireTreeOID + `","url":"u","truncated":false,"tree":[`)
	for index := 0; index < maxStateV3SpineEntries; index++ {
		if index != 0 {
			document.WriteByte(',')
		}
		fmt.Fprintf(&document, `{"path":"directory/%05d","mode":"040000","type":"tree","sha":"%s","url":"u"}`, index, wireOID)
	}
	fmt.Fprintf(&document, `,{"path":"state.json","mode":"100644","type":"blob","sha":"%s","size":1,"url":"u"}]}`, wireOID)
	tree, decoded := decodeTree([]byte(document.String()), wireTreeOID)
	if !decoded {
		t.Fatal("strict-valid mixed tree rejected")
	}

	snapshot, ok := stateV3SnapshotEvidence(raw, rest, graphQL, tree, policy, false, &stateV3SpineBudget{})
	if !ok || len(snapshot.Tree.Entries) != 1 || cap(snapshot.Tree.Entries) != 1 {
		t.Fatalf("projection accepted=%t len=%d cap=%d", ok, len(snapshot.Tree.Entries), cap(snapshot.Tree.Entries))
	}
}

func TestStateV3SpineBudgetAccountsReturnedSliceCapacities(t *testing.T) {
	budget := stateV3SpineBudget{}
	if !reserveStateV3Snapshots(&budget, 2) {
		t.Fatal("snapshot backing capacity rejected")
	}
	remaining := maxStateV3SpineBytes - budget.bytes
	if !budget.reserve(0, remaining) || reserveStateV3Snapshots(&budget, 1) {
		t.Fatal("snapshot backing capacity was not aggregated")
	}

	paths := make([]gardenerrelease.StateV3ChangedPath, 1, 128)
	paths[0] = gardenerrelease.StateV3ChangedPath{Path: "x", ParentOID: wireParentOID, ChildOID: wireOID}
	spine, result := stateV3SpineChanges(stateV3Spine{snapshots: []gardenerrelease.StateV3StateSnapshot{
		{Tree: gardenerrelease.StateV3StateTreeEvidence{OID: wireTreeOID, Complete: true, Entries: []gardenerrelease.StateV3StateTreeEntry{{Path: "x", Mode: "100644", Type: "blob", OID: wireOID}}}, Commit: gardenerrelease.StateV3StateCommitEvidence{OID: wireOID, TreeOID: wireTreeOID}},
		{Tree: gardenerrelease.StateV3StateTreeEvidence{OID: wireParentOID, Complete: true, Entries: []gardenerrelease.StateV3StateTreeEntry{{Path: "x", Mode: "100644", Type: "blob", OID: wireParentOID}}}, Commit: gardenerrelease.StateV3StateCommitEvidence{OID: wireParentOID, TreeOID: wireParentOID}},
	}}, &stateV3SpineBudget{})
	if result.Diagnostic != DiagnosticOK || len(spine.snapshots[0].Commit.ChangedPaths) != len(paths) || cap(spine.snapshots[0].Commit.ChangedPaths) != len(paths) {
		t.Fatalf("changes result=%#v len=%d cap=%d", result, len(spine.snapshots[0].Commit.ChangedPaths), cap(spine.snapshots[0].Commit.ChangedPaths))
	}
}

func TestStateV3SnapshotEvidenceBindsCheckpointParent(t *testing.T) {
	raw, rest, graphQL, tree, policy := validSpineEvidence()
	raw.Parents = nil
	rest.Parents = nil
	snapshot, ok := stateV3SnapshotEvidence(raw, rest, graphQL, tree, policy, true, &stateV3SpineBudget{})
	if !ok || snapshot.Commit.ParentOID != "" {
		t.Fatalf("checkpoint parent=%q accepted=%t", snapshot.Commit.ParentOID, ok)
	}

	raw, rest, graphQL, tree, policy = validSpineEvidence()
	snapshot, ok = stateV3SnapshotEvidence(raw, rest, graphQL, tree, policy, true, &stateV3SpineBudget{})
	if !ok || snapshot.Commit.ParentOID != "" {
		t.Fatalf("checkpoint with raw parent=%q accepted=%t", snapshot.Commit.ParentOID, ok)
	}
}

func TestStateV3SnapshotEvidenceRequiresIndependentAgreement(t *testing.T) {
	raw, rest, graphQL, tree, policy := validSpineEvidence()
	snapshot, ok := stateV3SnapshotEvidence(raw, rest, graphQL, tree, policy, false, &stateV3SpineBudget{})
	if !ok || snapshot.Commit.OID != raw.SHA || snapshot.Commit.ParentOID != wireParentOID || len(snapshot.Tree.Entries) != 1 {
		t.Fatalf("valid evidence rejected: %#v", snapshot)
	}

	for name, mutate := range map[string]func(*wireRawCommit, *wireRESTCommit, *wireGQLCommit, *wireTree, *gardenerrelease.StateV3Policy){
		"REST message": func(_ *wireRawCommit, rest *wireRESTCommit, _ *wireGQLCommit, _ *wireTree, _ *gardenerrelease.StateV3Policy) {
			rest.Commit.Message = "other"
		},
		"GraphQL author": func(_ *wireRawCommit, _ *wireRESTCommit, gql *wireGQLCommit, _ *wireTree, _ *gardenerrelease.StateV3Policy) {
			gql.Data.Repository.Object.Author.Email = "other@example.invalid"
		},
		"REST parent": func(_ *wireRawCommit, rest *wireRESTCommit, _ *wireGQLCommit, _ *wireTree, _ *gardenerrelease.StateV3Policy) {
			rest.Parents[0].SHA = wireOID
		},
		"tree OID": func(_ *wireRawCommit, _ *wireRESTCommit, _ *wireGQLCommit, tree *wireTree, _ *gardenerrelease.StateV3Policy) {
			tree.SHA = wireOID
		},
		"executable leaf": func(_ *wireRawCommit, _ *wireRESTCommit, _ *wireGQLCommit, tree *wireTree, _ *gardenerrelease.StateV3Policy) {
			tree.Tree[0].Mode = "100755"
		},
		"policy role": func(_ *wireRawCommit, _ *wireRESTCommit, _ *wireGQLCommit, _ *wireTree, policy *gardenerrelease.StateV3Policy) {
			policy.CommitRoles.AuthorRaw.Name = "other"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidateRaw := cloneRawCommit(raw)
			candidateREST := cloneRESTCommit(rest)
			candidateGraphQL := cloneGQLCommit(graphQL)
			candidateTree := cloneTree(tree)
			candidatePolicy := policy
			mutate(&candidateRaw, &candidateREST, &candidateGraphQL, &candidateTree, &candidatePolicy)
			if _, ok := stateV3SnapshotEvidence(candidateRaw, candidateREST, candidateGraphQL, candidateTree, candidatePolicy, false, &stateV3SpineBudget{}); ok {
				t.Fatal("contradictory evidence accepted")
			}
		})
	}
}

func TestStateV3SnapshotEvidenceRejectsFailedSignatureSemantics(t *testing.T) {
	for name, mutate := range map[string]func(*wireRESTCommit, *wireGQLCommit){
		"REST verified false": func(rest *wireRESTCommit, _ *wireGQLCommit) {
			rest.Commit.Verification.Verified = pointer(false)
		},
		"REST verified missing": func(rest *wireRESTCommit, _ *wireGQLCommit) {
			rest.Commit.Verification.Verified = nil
		},
		"REST reason": func(rest *wireRESTCommit, _ *wireGQLCommit) {
			rest.Commit.Verification.Reason = "unsigned"
		},
		"GraphQL valid false": func(_ *wireRESTCommit, graphQL *wireGQLCommit) {
			graphQL.Data.Repository.Object.Signature.IsValid = pointer(false)
		},
		"GraphQL valid missing": func(_ *wireRESTCommit, graphQL *wireGQLCommit) {
			graphQL.Data.Repository.Object.Signature.IsValid = nil
		},
		"GraphQL GitHub signed false": func(_ *wireRESTCommit, graphQL *wireGQLCommit) {
			graphQL.Data.Repository.Object.Signature.WasSignedByGitHub = pointer(false)
		},
		"GraphQL GitHub signed missing": func(_ *wireRESTCommit, graphQL *wireGQLCommit) {
			graphQL.Data.Repository.Object.Signature.WasSignedByGitHub = nil
		},
		"GraphQL state": func(_ *wireRESTCommit, graphQL *wireGQLCommit) {
			graphQL.Data.Repository.Object.Signature.State = "INVALID"
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw, rest, graphQL, tree, policy := validSpineEvidence()
			mutate(&rest, &graphQL)
			if _, ok := stateV3SnapshotEvidence(raw, rest, graphQL, tree, policy, false, &stateV3SpineBudget{}); ok {
				t.Fatal("invalid verification evidence accepted")
			}
		})
	}
}

func TestStateV3SpineOwnsMetadataAndSnapshotStrings(t *testing.T) {
	refSource := strings.Repeat("x", 1<<20) + gardenerrelease.StateV3MinorStateRef
	policy := validStateV3Policy(t)
	checkpointSource := strings.Repeat("y", 1<<20) + policy.StateLanes.Minor.CheckpointOID
	ref := refSource[len(refSource)-len(gardenerrelease.StateV3MinorStateRef):]
	checkpoint := checkpointSource[len(checkpointSource)-len(policy.StateLanes.Minor.CheckpointOID):]
	policy.StateLanes.Minor.StateRef = ref
	policy.StateLanes.Minor.CheckpointOID = checkpoint
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatalf("oversized-backed valid policy rejected: %v", err)
	}
	if got := stateV3StringsBytes(ref, checkpoint); got != len(ref)+len(checkpoint) {
		t.Fatalf("metadata accounting got=%d want=%d", got, len(ref)+len(checkpoint))
	}

	spine := stateV3Spine{stateRef: strings.Clone(ref), checkpointOID: strings.Clone(checkpoint)}
	if unsafe.StringData(spine.stateRef) == unsafe.StringData(ref) || unsafe.StringData(spine.checkpointOID) == unsafe.StringData(checkpoint) {
		t.Fatal("spine metadata retained oversized source backing storage")
	}

	raw, rest, graphQL, tree, evidencePolicy := validSpineEvidence()
	reasonSource := strings.Repeat("r", 1<<20) + gardenerrelease.StateV3RequiredRESTVerificationReason
	stateSource := strings.Repeat("s", 1<<20) + gardenerrelease.StateV3RequiredSignatureState
	rest.Commit.Verification.Reason = reasonSource[len(reasonSource)-len(gardenerrelease.StateV3RequiredRESTVerificationReason):]
	graphQL.Data.Repository.Object.Signature.State = stateSource[len(stateSource)-len(gardenerrelease.StateV3RequiredSignatureState):]
	snapshot, ok := stateV3SnapshotEvidence(raw, rest, graphQL, tree, evidencePolicy, false, &stateV3SpineBudget{})
	if !ok {
		t.Fatal("valid oversized-backed verification strings rejected")
	}
	if unsafe.StringData(snapshot.Commit.RESTReason) == unsafe.StringData(rest.Commit.Verification.Reason) || unsafe.StringData(snapshot.Commit.SignatureState) == unsafe.StringData(graphQL.Data.Repository.Object.Signature.State) {
		t.Fatal("snapshot retained oversized source backing storage")
	}
}

func TestStateV3SpineChangesDerivesOnlyFromCompleteEvidence(t *testing.T) {
	_, _, _, _, policy := validSpineEvidence()
	parentTree := gardenerrelease.StateV3StateTreeEvidence{OID: wireParentOID, Complete: true, Entries: []gardenerrelease.StateV3StateTreeEntry{{Path: "x", Mode: "100644", Type: "blob", OID: wireOID}}}
	childTree := gardenerrelease.StateV3StateTreeEvidence{OID: wireTreeOID, Complete: true, Entries: []gardenerrelease.StateV3StateTreeEntry{{Path: "x", Mode: "100644", Type: "blob", OID: wireTreeOID}}}
	spine, result := stateV3SpineChanges(stateV3Spine{stateRef: policy.StateLanes.Minor.StateRef, checkpointOID: wireParentOID, snapshots: []gardenerrelease.StateV3StateSnapshot{{Commit: gardenerrelease.StateV3StateCommitEvidence{OID: wireOID, TreeOID: wireTreeOID}, Tree: childTree}, {Commit: gardenerrelease.StateV3StateCommitEvidence{OID: wireParentOID, TreeOID: wireParentOID}, Tree: parentTree}}}, &stateV3SpineBudget{})
	if result.Diagnostic != DiagnosticOK || len(spine.snapshots[0].Commit.ChangedPaths) != 1 {
		t.Fatalf("changes result=%#v spine=%#v", result, spine)
	}
	spine.snapshots[1].Tree.Complete = false
	if _, result := stateV3SpineChanges(spine, &stateV3SpineBudget{}); result.Diagnostic != DiagnosticResponseInvalid {
		t.Fatalf("incomplete tree result=%#v", result)
	}
}

func assertStateV3SpineSessionClosed(t *testing.T, session *Session) {
	t.Helper()
	if !session.closed || len(session.artifacts) != 0 || session.retainedBytes != 0 {
		t.Fatalf("closed=%t artifacts=%d retained=%d", session.closed, len(session.artifacts), session.retainedBytes)
	}
}

func spineRawJSON(oid, tree, parent string) string {
	parents := "[]"
	if parent != "" {
		parents = `[{"sha":"` + parent + `","url":"u","html_url":"u"}]`
	}
	identity := `{"name":"github-actions[bot]","email":"41898282+github-actions[bot]@users.noreply.github.com","date":"2026-01-02T03:04:05Z"}`
	committer := `{"name":"GitHub","email":"noreply@github.com","date":"2026-01-02T03:04:05Z"}`
	return `{"sha":"` + oid + `","node_id":"n","url":"u","html_url":"u","author":` + identity + `,"committer":` + committer + `,"tree":{"sha":"` + tree + `","url":"u"},"message":"state","parents":` + parents + `,"verification":` + wireVerificationJSON() + `}`
}

func spineRESTJSON(oid, tree, parent string) string {
	parents := "[]"
	if parent != "" {
		parents = `[{"sha":"` + parent + `","url":"u","html_url":"u"}]`
	}
	author := `{"name":"github-actions[bot]","email":"41898282+github-actions[bot]@users.noreply.github.com","date":"2026-01-02T03:04:05Z"}`
	committer := `{"name":"GitHub","email":"noreply@github.com","date":"2026-01-02T03:04:05Z"}`
	user := strings.Replace(wireRESTUserJSON(), `"login":"bot","id":1,"node_id"`, `"login":"synthetic-release-app[bot]","id":30003,"node_id"`, 1)
	user = strings.Replace(user, `"type":"User"`, `"type":"Bot"`, 1)
	committerUser := strings.Replace(wireRESTUserJSON(), `"login":"bot","id":1,"node_id"`, `"login":"synthetic-platform-committer","id":40004,"node_id"`, 1)
	return `{"sha":"` + oid + `","node_id":"n","url":"u","html_url":"u","comments_url":"u","commit":{"author":` + author + `,"committer":` + committer + `,"message":"state","tree":{"sha":"` + tree + `","url":"u"},"url":"u","comment_count":0,"verification":` + wireVerificationJSON() + `},"author":` + user + `,"committer":` + committerUser + `,"parents":` + parents + `,"stats":{"total":0,"additions":0,"deletions":0},"files":[]}`
}

func spineGraphQLJSON(oid string) string {
	author := `{"name":"github-actions[bot]","email":"41898282+github-actions[bot]@users.noreply.github.com","date":"2026-01-02T03:04:05Z","user":{"__typename":"User","login":"synthetic-release-app[bot]","databaseId":30003}}`
	committer := `{"name":"GitHub","email":"noreply@github.com","date":"2026-01-02T03:04:05Z","user":null}`
	signer := `{"__typename":"User","login":"synthetic-platform-signer","databaseId":50005}`
	return `{"data":{"repository":{"object":{"__typename":"Commit","oid":"` + oid + `","author":` + author + `,"committer":` + committer + `,"signature":{"isValid":true,"state":"VALID","wasSignedByGitHub":true,"signer":` + signer + `}}}}}`
}

func spineTreeJSON(tree, blob string) string {
	return `{"sha":"` + tree + `","url":"u","truncated":false,"tree":[{"path":"state.json","mode":"100644","type":"blob","sha":"` + blob + `","size":1,"url":"u"}]}`
}

func validStateV3Policy(t *testing.T) gardenerrelease.StateV3Policy {
	t.Helper()
	file, err := os.Open("testdata/valid_state_v3_policy.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var policy gardenerrelease.StateV3Policy
	if err := json.NewDecoder(reader).Decode(&policy); err != nil {
		t.Fatal(err)
	}
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatalf("fixture policy: %v", err)
	}
	return policy
}

func validSpineEvidence() (wireRawCommit, wireRESTCommit, wireGQLCommit, wireTree, gardenerrelease.StateV3Policy) {
	raw := wireRawCommit{SHA: wireOID, Tree: wireTreeRef{SHA: wireTreeOID}, Message: "state"}
	raw.Author = wireIdentity{Name: "author", Email: "author@example.invalid", Date: "2026-01-01T00:00:00Z"}
	raw.Committer = wireIdentity{Name: "committer", Email: "committer@example.invalid", Date: "2026-01-01T00:00:00Z"}
	raw.Parents = []wireParent{{SHA: wireParentOID}}
	rest := wireRESTCommit{SHA: raw.SHA, Parents: append([]wireParent(nil), raw.Parents...)}
	rest.Commit.Author, rest.Commit.Committer, rest.Commit.Message, rest.Commit.Tree = raw.Author, raw.Committer, raw.Message, raw.Tree
	rest.Author = &wireRESTIdentity{Login: "author-bot", ID: 1, Type: "Bot"}
	rest.Committer = &wireRESTIdentity{Login: "committer-user", ID: 2, Type: "User"}
	rest.Commit.Verification.Verified = pointer(true)
	rest.Commit.Verification.Reason = "valid"
	var graphQL wireGQLCommit
	object := &graphQL.Data.Repository.Object
	object.OID = raw.SHA
	object.Author = wireGQLActor{Name: raw.Author.Name, Email: raw.Author.Email, Date: raw.Author.Date, User: &wireGQLIdentity{Typename: "User", Login: "author-bot", DatabaseID: pointer(int64(1))}}
	object.Committer = wireGQLActor{Name: raw.Committer.Name, Email: raw.Committer.Email, Date: raw.Committer.Date, User: &wireGQLIdentity{Typename: "User", Login: "committer-user", DatabaseID: pointer(int64(2))}}
	object.Signature.IsValid, object.Signature.WasSignedByGitHub, object.Signature.State = pointer(true), pointer(true), "VALID"
	object.Signature.Signer = &wireGQLIdentity{Typename: "User", Login: "signer", DatabaseID: pointer(int64(3))}
	tree := wireTree{SHA: raw.Tree.SHA, Tree: []wireTreeEntry{{Path: "x", Mode: "100644", Type: "blob", SHA: wireOID, Size: pointer(int64(1))}}}
	policy := gardenerrelease.StateV3Policy{CommitRoles: gardenerrelease.StateV3CommitRoles{
		AuthorRaw:              gardenerrelease.StateV3RawIdentity{Name: raw.Author.Name, Email: raw.Author.Email},
		AuthorREST:             gardenerrelease.StateV3AssociatedIdentity{Login: "author-bot", DatabaseID: "1", Type: "Bot"},
		AuthorGraphQL:          gardenerrelease.StateV3AssociatedIdentity{Login: "author-bot", DatabaseID: "1", Type: "User"},
		CommitterRaw:           gardenerrelease.StateV3RawIdentity{Name: raw.Committer.Name, Email: raw.Committer.Email},
		CommitterREST:          gardenerrelease.StateV3OptionalAssociatedIdentity{Present: true, Identity: gardenerrelease.StateV3AssociatedIdentity{Login: "committer-user", DatabaseID: "2", Type: "User"}},
		CommitterGraphQL:       gardenerrelease.StateV3OptionalAssociatedIdentity{Present: true, Identity: gardenerrelease.StateV3AssociatedIdentity{Login: "committer-user", DatabaseID: "2", Type: "User"}},
		SignatureSignerGraphQL: gardenerrelease.StateV3AssociatedIdentity{Login: "signer", DatabaseID: "3", Type: "User"},
	}}
	return raw, rest, graphQL, tree, policy
}
