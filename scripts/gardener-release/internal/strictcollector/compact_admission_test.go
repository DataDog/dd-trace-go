// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

func TestStateV3CompactLaneHistoryOwnsOnlyFixedMetadata(t *testing.T) {
	var history stateV3CompactLaneHistory
	if got, want := len(history.snapshots), gardenerrelease.MaxStateV3StateLaneHistoryCommits; got != want {
		t.Fatalf("compact snapshot capacity=%d want=%d", got, want)
	}
	assertFixedCompactType(t, reflect.TypeFor[stateV3CompactLaneHistory]())
}

func assertFixedCompactType(t *testing.T, typ reflect.Type) {
	t.Helper()
	switch typ.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.String, reflect.Interface, reflect.Func:
		t.Fatalf("compact continuation retains dynamic %s in %s", typ.Kind(), typ)
	case reflect.Array:
		assertFixedCompactType(t, typ.Elem())
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			assertFixedCompactType(t, typ.Field(i).Type)
		}
	}
}

func TestStateV3CompactAdmissionBindsCompactedSnapshot(t *testing.T) {
	history := &stateV3CompactLaneHistory{count: 4}
	child := &stateV3AssemblyChild{session: &session{assemblyLive: true, assemblyRole: stateV3AssemblyMinor, compactHistory: history, compactGeneration: 7}}
	admission := &stateV3LaneHistoryAdmission{child: child, generation: 7}
	commit, _ := decodeFixedOID(documentTestCommitOID)
	tree, _ := decodeFixedOID(documentTestTreeOID)
	blob, _ := decodeFixedOID(documentTestBlobOID)
	snapshot := &history.snapshots[3]
	*snapshot = stateV3CompactSnapshot{commitOID: commit, treeOID: tree, ordinal: 3, count: 1}
	snapshot.entries[0] = stateV3CompactDocumentEntry{oid: blob, index: 2, kind: stateV3DocumentRecord, len: uint8(len("requests/1/2/state.json"))}
	copy(snapshot.entries[0].path[:], "requests/1/2/state.json")
	entry, entryOK := compactEntry(snapshot, 0, stateV3AssemblyMinor)
	if !entryOK {
		t.Fatal("could not derive compact entry")
	}
	token, ok := admission.admitCompact(3, 0)
	if !ok {
		t.Fatal("compact entry was not admitted")
	}
	entry.admission = token
	if !token.permits(entry) {
		t.Fatal("compact token did not permit exact entry")
	}
	entry.oid = hexFixedOID(blob)
	entry.index++
	if token.permits(entry) {
		t.Fatal("compact token permitted altered entry")
	}
}

func TestStateV3CompactEntryLookupReturnsNoMutableHistoryReference(t *testing.T) {
	originalOID := gitBlobOID([]byte("original document bytes"))
	selectedOID := compactAdmissionOID(99999)
	calls := 0
	policy := compactStagedPolicy(t)
	child, operation := compactAdmissionChild(t, policy, roundTrip(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Path != "/repos/"+repository+"/git/blobs/"+originalOID {
			t.Fatalf("compact lookup dispatched caller-selected route %s", request.URL.Path)
		}
		return response(compactAdmissionBlobJSON(originalOID, []byte("original document bytes"))), nil
	}))
	defer operation.close()

	commit, _ := decodeFixedOID(documentTestCommitOID)
	tree, _ := decodeFixedOID(documentTestTreeOID)
	blob, _ := decodeFixedOID(originalOID)
	history := &stateV3CompactLaneHistory{count: 1}
	history.snapshots[0] = stateV3CompactSnapshot{commitOID: commit, treeOID: tree, ordinal: 0, count: 1}
	history.snapshots[0].entries[0] = stateV3CompactDocumentEntry{oid: blob, index: 2, kind: stateV3DocumentRecord, len: uint8(len("requests/1/2/state.json"))}
	copy(history.snapshots[0].entries[0].path[:], "requests/1/2/state.json")
	child.session.mu.Lock()
	child.session.compactHistory, child.session.compactGeneration = history, 91
	child.session.mu.Unlock()
	defer func() {
		child.session.mu.Lock()
		child.session.compactHistory = nil
		child.session.mu.Unlock()
	}()

	entry, ok := child.session.compactEntry(91, 0, 0)
	if !ok {
		t.Fatal("could not resolve compact entry")
	}
	entry.oid = selectedOID
	if _, result := child.session.readAssemblyCompactBlob(91, 0, 0); result.Diagnostic != DiagnosticOK {
		t.Fatalf("blob read result=%#v", result)
	}
	if calls != 1 {
		t.Fatalf("blob calls=%d", calls)
	}
}

func TestStateV3CompactHistoryRequiresExactCheckpoint(t *testing.T) {
	checkpoint, _ := decodeFixedOID(documentTestCommitOID)
	notCheckpoint, _ := decodeFixedOID(documentTestTreeOID)
	history := stateV3CompactLaneHistory{count: 1}
	history.snapshots[0].commitOID = notCheckpoint
	if historyReachedCheckpoint(&history, documentTestCommitOID) {
		t.Fatal("non-checkpoint compact suffix was accepted")
	}
	history.snapshots[0].commitOID = checkpoint
	if !historyReachedCheckpoint(&history, documentTestCommitOID) {
		t.Fatal("exact checkpoint was rejected")
	}
}

func TestStateV3CompactAdmissionRejectsMaximumDepthNonCheckpointBeforeBlobDispatch(t *testing.T) {
	policy := validStateV3Policy(t)
	checkpoint := compactAdmissionOID(9000)
	policy.StateLanes.Minor.CheckpointOID = checkpoint
	policy.StateLanes.Minor.MaxHistoryCommits = gardenerrelease.MaxStateV3StateLaneHistoryCommits
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatalf("policy: %v", err)
	}

	var blobCalls, rawCalls int
	head := compactAdmissionOID(1)
	child, operation := compactAdmissionChild(t, policy, roundTrip(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/git/blobs/") {
			blobCalls++
			t.Fatalf("non-checkpoint history dispatched blob request %s", request.URL.Path)
		}
		switch {
		case request.URL.Path == "/repos/"+repository+"/git/ref/heads/gardener-release-state/minor":
			return response(compactAdmissionRefJSON(gardenerrelease.StateV3MinorStateRef, head)), nil
		case strings.HasPrefix(request.URL.Path, "/repos/"+repository+"/git/commits/"):
			rawCalls++
			oid := strings.TrimPrefix(request.URL.Path, "/repos/"+repository+"/git/commits/")
			index := compactAdmissionOIDIndex(oid)
			parent := ""
			if index <= gardenerrelease.MaxStateV3StateLaneHistoryCommits {
				parent = compactAdmissionOID(index + 1)
			}
			return response(spineRawJSON(oid, compactAdmissionOID(10_000+index), parent)), nil
		case strings.HasPrefix(request.URL.Path, "/repos/"+repository+"/commits/"):
			oid := strings.TrimPrefix(request.URL.Path, "/repos/"+repository+"/commits/")
			index := compactAdmissionOIDIndex(oid)
			parent := ""
			if index <= gardenerrelease.MaxStateV3StateLaneHistoryCommits {
				parent = compactAdmissionOID(index + 1)
			}
			return response(spineRESTJSON(oid, compactAdmissionOID(10_000+index), parent)), nil
		case request.URL.Path == "/graphql":
			return response(spineGraphQLJSON(compactAdmissionGraphQLOID(request))), nil
		case strings.HasPrefix(request.URL.Path, "/repos/"+repository+"/git/trees/"):
			tree := strings.TrimPrefix(request.URL.Path, "/repos/"+repository+"/git/trees/")
			return response(compactAdmissionTreeJSON(tree, nil)), nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	}))
	defer operation.close()

	result := child.collectStateV3LaneDocuments()
	if result.Diagnostic != DiagnosticRequiredEvidenceAbsent {
		t.Fatalf("result=%#v raw_calls=%d", result, rawCalls)
	}
	compactAdmissionStoreIsZero(t, operation.store)
	if blobCalls != 0 {
		t.Fatalf("blob calls=%d", blobCalls)
	}
}

func TestStateV3CompactAdmissionRejectsDirectoryOnlyCheckpointBeforeBlobDispatch(t *testing.T) {
	policy := validStateV3Policy(t)
	checkpoint := compactAdmissionOID(250)
	policy.StateLanes.Minor.CheckpointOID = checkpoint
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatal(err)
	}
	child, operation := compactAdmissionChild(t, policy, roundTrip(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/git/blobs/") {
			t.Fatal("directory-only checkpoint dispatched a blob")
		}
		switch {
		case request.URL.Path == "/repos/"+repository+"/git/ref/heads/gardener-release-state/minor":
			return response(compactAdmissionRefJSON(gardenerrelease.StateV3MinorStateRef, checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+checkpoint:
			return response(spineRawJSON(checkpoint, compactAdmissionOID(251), "")), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+checkpoint:
			return response(spineRESTJSON(checkpoint, compactAdmissionOID(251), "")), nil
		case request.URL.Path == "/graphql":
			return response(spineGraphQLJSON(checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(251):
			return response(`{"sha":"` + compactAdmissionOID(251) + `","url":"u","truncated":false,"tree":[{"path":"unexpected","mode":"040000","type":"tree","sha":"` + compactAdmissionOID(252) + `","url":"u"}]}`), nil
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
			return nil, nil
		}
	}))
	defer operation.close()
	if result := child.collectStateV3LaneDocuments(); result.Diagnostic != DiagnosticRequiredEvidenceAbsent {
		t.Fatalf("result=%#v", result)
	}
	compactAdmissionStoreIsZero(t, operation.store)
}

func TestStateV3LaneTerminationPrefixRejectsDirectoryOnlyCleanup(t *testing.T) {
	completeOID, cleanupOID := mustFixedOID(compactAdmissionOID(260)), mustFixedOID(compactAdmissionOID(261))
	history := stateV3CompactLaneHistory{count: 2}
	history.snapshots[0] = stateV3CompactSnapshot{commitOID: cleanupOID, parentOID: completeOID, ordinal: 0, treeEmpty: false}
	history.snapshots[1] = stateV3CompactSnapshot{commitOID: completeOID, ordinal: 1}
	s := newTestSession(nil, time.Now)
	s.assemblyLive, s.assemblyRole, s.compactGeneration, s.compactHistory = true, stateV3AssemblyMinor, 1, &history
	prefixes := stateV3LaneTerminationPrefixes{}
	child := stateV3AssemblyChild{session: s, store: &stateV3DocumentStore{}, terminations: &prefixes}
	if result := child.captureLaneTerminationPrefix(1, 1, 0); result.Diagnostic != DiagnosticOK {
		t.Fatalf("result=%#v", result)
	}
	if prefixes != (stateV3LaneTerminationPrefixes{}) {
		t.Fatal("directory-only cleanup issued a termination prefix")
	}
}

func TestStateV3CompactAdmissionResetsStoreAfterMalformedLaterTransition(t *testing.T) {
	policy := validStateV3Policy(t)
	checkpoint, childOID, head := compactAdmissionOID(300), compactAdmissionOID(301), compactAdmissionOID(302)
	policy.StateLanes.Minor.CheckpointOID = checkpoint
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatalf("policy: %v", err)
	}
	leaseRaw := []byte(`{"schema_version":"1","repository_id":"","repository_full_name":"","original_comment_id":"","command":"","requested_version":"","resolved_version":"","source_ref":"","source_oid":"","request_key":"","request_sha256":"","reservation_marker":"","version_resolution_sha256":"","coordination_ref":"","coordination_claim_path":"","coordination_claim_oid":"","coordination_claim_blob_oid":"","coordination_claim_sha256":"","coordination_parent_oid":""}`)
	leaseOID := gitBlobOID(leaseRaw)
	var blobCalls int
	tree := compactAdmissionOID(400)
	child, operation := compactAdmissionChild(t, policy, roundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/repos/"+repository+"/git/ref/heads/gardener-release-state/minor":
			return response(compactAdmissionRefJSON(gardenerrelease.StateV3MinorStateRef, head)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+head:
			return response(spineRawJSON(head, tree, childOID)), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+head:
			return response(spineRESTJSON(head, tree, childOID)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+childOID:
			return response(spineRawJSON(childOID, tree, checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+childOID:
			return response(spineRESTJSON(childOID, tree, checkpoint)), nil
		case request.URL.Path == "/repos/"+repository+"/git/commits/"+checkpoint:
			return response(spineRawJSON(checkpoint, compactAdmissionOID(401), "")), nil
		case request.URL.Path == "/repos/"+repository+"/commits/"+checkpoint:
			return response(spineRESTJSON(checkpoint, compactAdmissionOID(401), "")), nil
		case request.URL.Path == "/graphql":
			return response(spineGraphQLJSON(compactAdmissionGraphQLOID(request))), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+tree:
			return response(compactAdmissionTreeJSON(tree, []compactAdmissionTreeEntry{{path: gardenerrelease.StateV3ActiveLeasePath, oid: leaseOID, raw: leaseRaw}})), nil
		case request.URL.Path == "/repos/"+repository+"/git/trees/"+compactAdmissionOID(401):
			return response(compactAdmissionTreeJSON(compactAdmissionOID(401), nil)), nil
		case request.URL.Path == "/repos/"+repository+"/git/blobs/"+leaseOID:
			blobCalls++
			return response(compactAdmissionBlobJSON(leaseOID, leaseRaw)), nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	}))
	defer operation.close()

	policy.StateLanes.Minor.MaxHistoryCommits = 3
	result := child.collectStateV3LaneDocuments()
	if result.Diagnostic != DiagnosticResponseInvalid {
		t.Fatalf("result=%#v", result)
	}
	compactAdmissionStoreIsZero(t, operation.store)
	if blobCalls == 0 {
		t.Fatal("valid-looking prefix did not reach the real blob transport path")
	}
}

type compactAdmissionTreeEntry struct {
	path, oid string
	raw       []byte
}

func compactAdmissionChild(t *testing.T, policy gardenerrelease.StateV3Policy, transport http.RoundTripper) (*stateV3AssemblyChild, *stateV3AssemblyOperation) {
	t.Helper()
	noCall := roundTrip(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected non-minor request %s", request.URL.Path)
		return nil, nil
	})
	operation := &stateV3AssemblyOperation{
		minor:        newTestSession(transport, time.Now),
		patch:        newTestSession(noCall, time.Now),
		coordination: newTestSession(noCall, time.Now),
	}
	if result := operation.begin(t.Context(), time.Now().Add(time.Minute), policy); result.Diagnostic != DiagnosticOK {
		t.Fatalf("begin: %#v", result)
	}
	child, result := operation.beginChild(stateV3AssemblyMinor)
	if result.Diagnostic != DiagnosticOK {
		t.Fatalf("begin child: %#v", result)
	}
	return child, operation
}

func compactAdmissionOID(index int) string { return fmt.Sprintf("%040x", index) }
func compactAdmissionOIDIndex(oid string) int {
	var index int
	if _, err := fmt.Sscanf(oid, "%x", &index); err != nil {
		panic(err)
	}
	return index
}
func compactAdmissionGraphQLOID(request *http.Request) string {
	if request.Body == nil {
		panic("missing GraphQL request body")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		panic(err)
	}
	for index := 1; index <= 455; index++ {
		candidate := compactAdmissionOID(index)
		if strings.Contains(string(body), candidate) {
			return candidate
		}
	}
	panic("GraphQL request did not contain a known commit OID")
}
func compactStagedGraphQLOID(request *http.Request, snapshots []compactStagedSnapshot) string {
	if request.Body == nil {
		panic("missing GraphQL request body")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		panic(err)
	}
	for _, snapshot := range snapshots {
		if strings.Contains(string(body), snapshot.commit) {
			return snapshot.commit
		}
	}
	panic("GraphQL request did not contain a staged-fixture commit OID")
}

func compactAdmissionRefJSON(ref, oid string) string {
	return `{"ref":"` + ref + `","node_id":"n","url":"u","object":{"sha":"` + oid + `","type":"commit","url":"u"}}`
}
func compactAdmissionTreeJSON(tree string, entries []compactAdmissionTreeEntry) string {
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		values = append(values, `{"path":"`+entry.path+`","mode":"100644","type":"blob","sha":"`+entry.oid+`","size":`+fmt.Sprint(len(entry.raw))+`,"url":"u"}`)
	}
	return `{"sha":"` + tree + `","url":"u","truncated":false,"tree":[` + strings.Join(values, ",") + `]}`
}
func compactAdmissionBlobJSON(oid string, raw []byte) string {
	encoded := base64.StdEncoding.EncodeToString(raw)
	var content strings.Builder
	for len(encoded) > 76 {
		content.WriteString(encoded[:76])
		content.WriteString(`\n`)
		encoded = encoded[76:]
	}
	content.WriteString(encoded)
	return `{"sha":"` + oid + `","node_id":"n","size":` + fmt.Sprint(len(raw)) + `,"url":"u","content":"` + content.String() + `","encoding":"base64"}`
}
func compactAdmissionStoreIsZero(t *testing.T, store *stateV3DocumentStore) {
	t.Helper()
	if store == nil || store.blobCount != 0 || store.bindingCount != 0 || store.rawUsed != 0 {
		t.Fatalf("store retained blobs=%d bindings=%d raw=%d", store.blobCount, store.bindingCount, store.rawUsed)
	}
}

func TestStateV3LaneAdmissionOperationWindowBoundaryUsesConnectedFixedRoute(t *testing.T) {
	policy := compactStagedPolicy(t)
	policy.StateLanes.Minor.CheckpointOID = compactAdmissionOID(90_000)
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatalf("policy: %v", err)
	}

	t.Run("exact limit", func(t *testing.T) {
		// Twenty-five complete operations plus one active prepared operation
		// exactly fill the history topology: checkpoint + 25*18 + 4 = 455.
		// Every acquisition is connected through the preceding cleanup tree.
		snapshots := compactWindowBoundarySnapshots(t, policy.StateLanes.Minor.CheckpointOID, gardenerrelease.MaxStateV3LaneOperationWindows-1, true)
		if got, want := len(snapshots), gardenerrelease.MaxStateV3StateLaneHistoryCommits; got != want {
			t.Fatalf("snapshots=%d want=%d", got, want)
		}
		// Count logical record versions, coalescing the unchanged reserved record
		// across the envelope-materialization snapshot but resetting at cleanup.
		recordVersions, priorRecord := 0, ""
		for _, snapshot := range snapshots {
			record := ""
			for _, entry := range snapshot.entries {
				if strings.HasSuffix(entry.path, "/state.json") {
					record = entry.oid
				}
			}
			if record != "" && record != priorRecord {
				recordVersions++
			}
			priorRecord = record
		}
		if recordVersions != 377 {
			t.Fatalf("state record versions=%d want=377", recordVersions)
		}
		child, operation, requests, blobRequests := compactWindowBoundaryChild(t, policy, snapshots)
		defer operation.close()

		if result := child.collectStateV3LaneDocuments(); result.Diagnostic != DiagnosticOK {
			t.Fatalf("result=%#v", result)
		}
		if operation.store.bindingCount == 0 || operation.store.rawUsed == 0 {
			t.Fatalf("exact-limit chronology retained no tree-bound documents: bindings=%d raw=%d", operation.store.bindingCount, operation.store.rawUsed)
		}
		if *blobRequests == 0 {
			t.Fatal("exact-limit chronology did not read any tree-bound documents")
		}
		if *requests > stateV3AssemblyStateReads || *requests > maxReads {
			t.Fatalf("requests=%d exceed state bound=%d", *requests, stateV3AssemblyStateReads)
		}
	})

	t.Run("one over", func(t *testing.T) {
		// A twenty-seventh connected root cannot fit behind the 455-snapshot
		// bound. The role-bound history/checkpoint gate rejects its suffix before
		// blob hydration; no separate root counter is needed or reachable.
		snapshots := compactWindowBoundarySnapshots(t, policy.StateLanes.Minor.CheckpointOID, gardenerrelease.MaxStateV3LaneOperationWindows, true)
		child, operation, requests, blobRequests := compactWindowBoundaryChild(t, policy, snapshots)
		defer operation.close()

		if result := child.collectStateV3LaneDocuments(); result.Diagnostic != DiagnosticRequiredEvidenceAbsent {
			t.Fatalf("result=%#v", result)
		}
		compactAdmissionStoreIsZero(t, operation.store)
		if *blobRequests != 0 {
			t.Fatalf("one-over chronology hydrated %d document blobs before rejection", *blobRequests)
		}
		if *requests > stateV3AssemblyStateReads || *requests > maxReads {
			t.Fatalf("requests=%d exceed state bound=%d", *requests, stateV3AssemblyStateReads)
		}
	})
}

type compactWindowFixtureEntry struct {
	Path string `json:"path"`
	Raw  []byte `json:"raw"`
}
type compactWindowFixtureSnapshot struct {
	Entries []compactWindowFixtureEntry `json:"entries"`
}

func compactWindowBoundarySnapshots(t *testing.T, checkpoint string, completed int, staged bool) []compactStagedSnapshot {
	t.Helper()
	raw, err := os.ReadFile("testdata/window_boundary_template.json")
	if err != nil {
		t.Fatal(err)
	}
	var template []compactWindowFixtureSnapshot
	if err := json.Unmarshal(raw, &template); err != nil {
		t.Fatal(err)
	}
	if len(template) != 19 || len(template[0].Entries) != 0 || len(template[18].Entries) != 0 {
		t.Fatal("unexpected canonical completed-operation template")
	}

	entries := func(snapshot compactWindowFixtureSnapshot) []compactAdmissionTreeEntry {
		result := make([]compactAdmissionTreeEntry, len(snapshot.Entries))
		for i, entry := range snapshot.Entries {
			if entry.Path == "" || len(entry.Raw) == 0 {
				t.Fatal("invalid canonical operation document")
			}
			result[i] = compactAdmissionTreeEntry{path: entry.Path, oid: gitBlobOID(entry.Raw), raw: entry.Raw}
		}
		return result
	}

	snapshots := []compactStagedSnapshot{{commit: checkpoint, tree: compactAdmissionOID(91_000)}}
	appendSnapshot := func(snapshot compactWindowFixtureSnapshot) {
		index := len(snapshots)
		snapshots = append(snapshots, compactStagedSnapshot{
			commit:  compactAdmissionOID(100_000 + index),
			tree:    compactAdmissionOID(200_000 + index),
			parent:  snapshots[index-1].commit,
			entries: entries(snapshot),
		})
	}
	for operation := 0; operation < completed; operation++ {
		for _, snapshot := range template[1:] {
			appendSnapshot(snapshot)
		}
	}
	if staged {
		// Include lease-only, reserved, materialized-envelope, and prepared
		// snapshots for the active operation. This is the four-snapshot active
		// prepared shape used by the fixed capacity formulas.
		for _, snapshot := range template[1:5] {
			appendSnapshot(snapshot)
		}
	}
	return snapshots
}

func compactWindowBoundaryChild(t *testing.T, policy gardenerrelease.StateV3Policy, snapshots []compactStagedSnapshot) (*stateV3AssemblyChild, *stateV3AssemblyOperation, *int, *int) {
	t.Helper()
	if len(snapshots) < 2 || snapshots[0].commit != policy.StateLanes.Minor.CheckpointOID {
		t.Fatal("invalid connected window-boundary chronology")
	}
	byCommit, byTree, byBlob := map[string]compactStagedSnapshot{}, map[string]compactStagedSnapshot{}, map[string][]byte{}
	for _, snapshot := range snapshots {
		byCommit[snapshot.commit], byTree[snapshot.tree] = snapshot, snapshot
		for _, entry := range snapshot.entries {
			if prior, exists := byBlob[entry.oid]; exists && !bytes.Equal(prior, entry.raw) {
				t.Fatalf("blob %s has conflicting fixture bytes", entry.oid)
			}
			byBlob[entry.oid] = entry.raw
		}
	}
	requests, blobRequests := 0, 0
	child, operation := compactAdmissionChild(t, policy, roundTrip(func(request *http.Request) (*http.Response, error) {
		requests++
		switch {
		case request.URL.Path == "/repos/"+repository+"/git/ref/heads/gardener-release-state/minor":
			return response(compactAdmissionRefJSON(gardenerrelease.StateV3MinorStateRef, snapshots[len(snapshots)-1].commit)), nil
		case strings.HasPrefix(request.URL.Path, "/repos/"+repository+"/git/commits/"):
			oid := strings.TrimPrefix(request.URL.Path, "/repos/"+repository+"/git/commits/")
			snapshot, ok := byCommit[oid]
			if !ok {
				t.Fatalf("unknown raw commit %s", oid)
			}
			return response(spineRawJSON(snapshot.commit, snapshot.tree, snapshot.parent)), nil
		case strings.HasPrefix(request.URL.Path, "/repos/"+repository+"/commits/"):
			oid := strings.TrimPrefix(request.URL.Path, "/repos/"+repository+"/commits/")
			snapshot, ok := byCommit[oid]
			if !ok {
				t.Fatalf("unknown REST commit %s", oid)
			}
			return response(spineRESTJSON(snapshot.commit, snapshot.tree, snapshot.parent)), nil
		case request.URL.Path == "/graphql":
			return response(spineGraphQLJSON(compactStagedGraphQLOID(request, snapshots))), nil
		case strings.HasPrefix(request.URL.Path, "/repos/"+repository+"/git/trees/"):
			tree := strings.TrimPrefix(request.URL.Path, "/repos/"+repository+"/git/trees/")
			snapshot, ok := byTree[tree]
			if !ok {
				t.Fatalf("unknown tree %s", tree)
			}
			return response(compactAdmissionTreeJSON(snapshot.tree, snapshot.entries)), nil
		case strings.HasPrefix(request.URL.Path, "/repos/"+repository+"/git/blobs/"):
			blobRequests++
			oid := strings.TrimPrefix(request.URL.Path, "/repos/"+repository+"/git/blobs/")
			raw, ok := byBlob[oid]
			if !ok {
				t.Fatalf("unknown blob %s", oid)
			}
			return response(compactAdmissionBlobJSON(oid, raw)), nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	}))
	return child, operation, &requests, &blobRequests
}

func TestStateV3CompactBlobReadRejectsSynthesizedSnapshotBeforeDispatch(t *testing.T) {
	calls := 0
	s := newTestSession(roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, nil
	}), time.Now)
	s.assemblyLive, s.assemblyRole = true, stateV3AssemblyMinor
	// There is intentionally no compact snapshot argument at this seam. A
	// synthetically populated snapshot cannot select a blob request because
	// reads resolve only session-owned authenticated history.
	if _, result := s.readAssemblyCompactBlob(1, 0, 0); result.Diagnostic != DiagnosticProtocol || calls != 0 {
		t.Fatalf("result=%#v calls=%d", result, calls)
	}
}

// compactStagedFixture holds canonical bytes captured from the parent package's
// valid state-v3 fixture. Keeping the exact bytes makes these tests exercise
// the strict wire route while avoiding a second implementation of canonical
// state-document construction in the collector package.
type compactStagedFixture struct {
	Reserved    []byte
	Prepared    []byte
	Lease       []byte
	Reservation []byte
	Manifest    []byte
	Bundle      []byte
}

func loadCompactStagedFixture(t *testing.T) compactStagedFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/staged_envelope_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var encoded map[string]string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	decode := func(name string) []byte {
		value, err := base64.StdEncoding.DecodeString(encoded[name])
		if err != nil || len(value) == 0 {
			t.Fatalf("decode %s: %v", name, err)
		}
		return value
	}
	return compactStagedFixture{Reserved: decode("RESERVED"), Prepared: decode("PREPARED"), Lease: decode("LEASE"), Reservation: decode("RESERVATION"), Manifest: decode("MANIFEST"), Bundle: decode("BUNDLE")}
}

type compactStagedSnapshot struct {
	commit, tree, parent string
	entries              []compactAdmissionTreeEntry
}

func TestStateV3CompactAdmissionRetainsMaterializedReservedEnvelope(t *testing.T) {
	fixture := loadCompactStagedFixture(t)
	child, operation := compactStagedTransportChild(t, fixture, nil)
	defer operation.close()

	result := child.collectStateV3LaneDocuments()
	if result.Diagnostic != DiagnosticOK {
		t.Fatalf("result=%#v", result)
	}
	if operation.store.blobCount != 6 || operation.store.bindingCount != 13 || operation.store.rawUsed == 0 {
		t.Fatalf("store blobs=%d bindings=%d raw=%d", operation.store.blobCount, operation.store.bindingCount, operation.store.rawUsed)
	}
}

func TestStateV3CompactAdmissionRejectsMalformedMaterializedEnvelope(t *testing.T) {
	for name, mutate := range map[string]func(*compactStagedFixture){
		"manifest identity differs from observed bundle": func(fixture *compactStagedFixture) {
			bundleOID := gitBlobOID(fixture.Bundle)
			fixture.Manifest = bytes.ReplaceAll(fixture.Manifest, []byte(bundleOID), []byte(compactAdmissionOID(900)))
		},
		"non-reserved record materializes envelope": func(fixture *compactStagedFixture) {
			fixture.Reserved = bytes.Replace(fixture.Reserved, []byte(`"phase":"reserved"`), []byte(`"phase":"prepared"`), 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := loadCompactStagedFixture(t)
			mutate(&fixture)
			child, operation := compactStagedTransportChild(t, fixture, nil)
			defer operation.close()

			result := child.collectStateV3LaneDocuments()
			if result.Diagnostic != DiagnosticResponseInvalid {
				t.Fatalf("result=%#v", result)
			}
			compactAdmissionStoreIsZero(t, operation.store)
		})
	}
}

func TestStateV3CompactAdmissionAcceptsCoherentAlternateMaterializedEnvelope(t *testing.T) {
	base := loadCompactStagedFixture(t)
	alternate := compactAlternatePreparedFixture(t, base)
	child, operation := compactStagedTransportChild(t, base, func(snapshots *[]compactStagedSnapshot) {
		(*snapshots)[3].entries = compactEnvelopeEntries(alternate, alternate.Reserved)
		(*snapshots)[4].entries = compactEnvelopeEntries(alternate, alternate.Prepared)
	})
	defer operation.close()

	if result := child.collectStateV3LaneDocuments(); result.Diagnostic != DiagnosticOK {
		t.Fatalf("result=%#v", result)
	}
	if operation.store.bindingCount == 0 {
		t.Fatal("coherent alternate envelope was not retained")
	}
}

func TestStateV3CompactAdmissionRejectsPreparedEnvelopeChangedAfterMaterialization(t *testing.T) {
	fixture := loadCompactStagedFixture(t)
	child, operation := compactStagedTransportChild(t, fixture, func(snapshots *[]compactStagedSnapshot) {
		alternate := compactAlternatePreparedFixture(t, fixture)
		// Keep the materialized reserved tree intact. Only the later prepared
		// tree changes to a separately coherent envelope.
		(*snapshots)[4].entries = compactEnvelopeEntries(alternate, alternate.Prepared)
	})
	defer operation.close()

	result := child.collectStateV3LaneDocuments()
	if result.Diagnostic != DiagnosticResponseInvalid {
		t.Fatalf("result=%#v", result)
	}
	compactAdmissionStoreIsZero(t, operation.store)
}

func compactEnvelopeEntries(fixture compactStagedFixture, record []byte) []compactAdmissionTreeEntry {
	return []compactAdmissionTreeEntry{
		{path: gardenerrelease.StateV3ActiveLeasePath, oid: gitBlobOID(fixture.Lease), raw: fixture.Lease},
		{path: "requests/123/789/state.json", oid: gitBlobOID(record), raw: record},
		{path: "requests/123/789/generation.bundle", oid: gitBlobOID(fixture.Bundle), raw: fixture.Bundle},
		{path: "requests/123/789/prepared.json", oid: gitBlobOID(fixture.Manifest), raw: fixture.Manifest},
		{path: "requests/123/789/reservation.json", oid: gitBlobOID(fixture.Reservation), raw: fixture.Reservation},
	}
}

func compactAlternatePreparedFixture(t *testing.T, fixture compactStagedFixture) compactStagedFixture {
	t.Helper()
	if !gardenerrelease.ValidateStateV3RecordDocument(fixture.Prepared) {
		t.Fatal("base prepared record was not canonical")
	}
	alternate := fixture
	alternate.Bundle = append([]byte(nil), fixture.Bundle...)
	alternate.Bundle[0] ^= 1 // Preserve the bundle's length and all size totals.
	newBundleOID := gitBlobOID(alternate.Bundle)
	newBundleDigest := fmt.Sprintf("%x", sha256.Sum256(alternate.Bundle))
	var manifest map[string]any
	if err := json.Unmarshal(fixture.Manifest, &manifest); err != nil {
		t.Fatal(err)
	}
	bundle, ok := manifest["bundle"].(map[string]any)
	if !ok {
		t.Fatal("fixture manifest has no bundle")
	}
	bundle["blob_oid"], bundle["sha256"] = newBundleOID, newBundleDigest
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	alternate.Manifest = manifestRaw
	if _, _, ok := gardenerrelease.DecodeStateV3PreparedManifestDocument(alternate.Manifest); !ok {
		t.Fatal("alternate manifest did not decode")
	}
	manifestState, manifestPlans, ok := gardenerrelease.DecodeStateV3PreparedManifestDocument(alternate.Manifest)
	if !ok {
		t.Fatal("alternate manifest did not decode")
	}
	manifestState.StateFiles = []gardenerrelease.StateV3PreparedFile{
		{Path: "requests/123/789/generation.bundle", BlobOID: gitBlobOID(alternate.Bundle), SHA256: fmt.Sprintf("%x", sha256.Sum256(alternate.Bundle)), SizeBytes: int64(len(alternate.Bundle))},
		{Path: "requests/123/789/prepared.json", BlobOID: gitBlobOID(alternate.Manifest), SHA256: fmt.Sprintf("%x", sha256.Sum256(alternate.Manifest)), SizeBytes: int64(len(alternate.Manifest))},
		{Path: "requests/123/789/reservation.json", BlobOID: gitBlobOID(alternate.Reservation), SHA256: fmt.Sprintf("%x", sha256.Sum256(alternate.Reservation)), SizeBytes: int64(len(alternate.Reservation))},
	}
	var record gardenerrelease.StateV3Record
	if err := json.Unmarshal(alternate.Reserved, &record); err != nil {
		t.Fatalf("alternate reserved record: %v", err)
	}
	record.Phase, record.Prepared, record.TagPlans = gardenerrelease.StateV3PhasePrepared, &manifestState, manifestPlans
	record, err = gardenerrelease.AppendStateV3RecordEvent(record, gardenerrelease.StateV3Event{Kind: gardenerrelease.StateV3EventPhaseAdvanced, Phase: gardenerrelease.StateV3PhasePrepared})
	if err != nil {
		t.Fatalf("append prepared event: %v", err)
	}
	recordRaw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var canonical map[string]any
	if err := json.Unmarshal(recordRaw, &canonical); err != nil {
		t.Fatal(err)
	}
	alternate.Prepared, err = json.Marshal(canonical)
	if err != nil || !gardenerrelease.ValidateStateV3RecordDocument(alternate.Prepared) {
		t.Fatalf("alternate prepared record is not canonical: %v", err)
	}
	return alternate
}

func compactStagedPolicy(t *testing.T) gardenerrelease.StateV3Policy {
	t.Helper()
	policy := validStateV3Policy(t)
	policy.StateLanes.Minor.MaxHistoryCommits = gardenerrelease.MaxStateV3StateLaneHistoryCommits
	if err := gardenerrelease.ValidateStateV3Policy(policy); err != nil {
		t.Fatalf("policy: %v", err)
	}
	return policy
}

func compactStagedTransportChild(t *testing.T, fixture compactStagedFixture, mutate func(*[]compactStagedSnapshot)) (*stateV3AssemblyChild, *stateV3AssemblyOperation) {
	t.Helper()
	checkpoint := compactStagedPolicy(t).StateLanes.Minor.CheckpointOID
	lease, reserved, staged, prepared := compactAdmissionOID(501), compactAdmissionOID(502), compactAdmissionOID(503), compactAdmissionOID(504)
	leaseOID, reservedOID, preparedOID := gitBlobOID(fixture.Lease), gitBlobOID(fixture.Reserved), gitBlobOID(fixture.Prepared)
	bundleOID, manifestOID, reservationOID := gitBlobOID(fixture.Bundle), gitBlobOID(fixture.Manifest), gitBlobOID(fixture.Reservation)
	statePath := "requests/123/789/state.json"
	entries := func(values ...compactAdmissionTreeEntry) []compactAdmissionTreeEntry { return values }
	snapshots := []compactStagedSnapshot{
		{commit: checkpoint, tree: compactAdmissionOID(1500)},
		{commit: lease, tree: compactAdmissionOID(1501), parent: checkpoint, entries: entries(compactAdmissionTreeEntry{path: gardenerrelease.StateV3ActiveLeasePath, oid: leaseOID, raw: fixture.Lease})},
		{commit: reserved, tree: compactAdmissionOID(1502), parent: lease, entries: entries(compactAdmissionTreeEntry{path: gardenerrelease.StateV3ActiveLeasePath, oid: leaseOID, raw: fixture.Lease}, compactAdmissionTreeEntry{path: statePath, oid: reservedOID, raw: fixture.Reserved})},
		{commit: staged, tree: compactAdmissionOID(1503), parent: reserved, entries: entries(compactAdmissionTreeEntry{path: gardenerrelease.StateV3ActiveLeasePath, oid: leaseOID, raw: fixture.Lease}, compactAdmissionTreeEntry{path: statePath, oid: reservedOID, raw: fixture.Reserved}, compactAdmissionTreeEntry{path: "requests/123/789/generation.bundle", oid: bundleOID, raw: fixture.Bundle}, compactAdmissionTreeEntry{path: "requests/123/789/prepared.json", oid: manifestOID, raw: fixture.Manifest}, compactAdmissionTreeEntry{path: "requests/123/789/reservation.json", oid: reservationOID, raw: fixture.Reservation})},
		{commit: prepared, tree: compactAdmissionOID(1504), parent: staged, entries: entries(compactAdmissionTreeEntry{path: gardenerrelease.StateV3ActiveLeasePath, oid: leaseOID, raw: fixture.Lease}, compactAdmissionTreeEntry{path: statePath, oid: preparedOID, raw: fixture.Prepared}, compactAdmissionTreeEntry{path: "requests/123/789/generation.bundle", oid: bundleOID, raw: fixture.Bundle}, compactAdmissionTreeEntry{path: "requests/123/789/prepared.json", oid: manifestOID, raw: fixture.Manifest}, compactAdmissionTreeEntry{path: "requests/123/789/reservation.json", oid: reservationOID, raw: fixture.Reservation})},
	}
	if mutate != nil {
		mutate(&snapshots)
	}
	byCommit, byTree, byBlob := map[string]compactStagedSnapshot{}, map[string]compactStagedSnapshot{}, map[string][]byte{}
	for _, snapshot := range snapshots {
		byCommit[snapshot.commit], byTree[snapshot.tree] = snapshot, snapshot
		for _, entry := range snapshot.entries {
			if prior, exists := byBlob[entry.oid]; exists && !bytes.Equal(prior, entry.raw) {
				t.Fatalf("blob %s has conflicting fixture bytes", entry.oid)
			}
			byBlob[entry.oid] = entry.raw
		}
	}
	policy := compactStagedPolicy(t)
	return compactAdmissionChild(t, policy, roundTrip(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/repos/"+repository+"/git/ref/heads/gardener-release-state/minor":
			return response(compactAdmissionRefJSON(gardenerrelease.StateV3MinorStateRef, prepared)), nil
		case strings.HasPrefix(request.URL.Path, "/repos/"+repository+"/git/commits/"):
			oid := strings.TrimPrefix(request.URL.Path, "/repos/"+repository+"/git/commits/")
			snapshot, ok := byCommit[oid]
			if !ok {
				t.Fatalf("unknown raw commit %s", oid)
			}
			return response(spineRawJSON(snapshot.commit, snapshot.tree, snapshot.parent)), nil
		case strings.HasPrefix(request.URL.Path, "/repos/"+repository+"/commits/"):
			oid := strings.TrimPrefix(request.URL.Path, "/repos/"+repository+"/commits/")
			snapshot, ok := byCommit[oid]
			if !ok {
				t.Fatalf("unknown REST commit %s", oid)
			}
			return response(spineRESTJSON(snapshot.commit, snapshot.tree, snapshot.parent)), nil
		case request.URL.Path == "/graphql":
			return response(spineGraphQLJSON(compactStagedGraphQLOID(request, snapshots))), nil
		case strings.HasPrefix(request.URL.Path, "/repos/"+repository+"/git/trees/"):
			tree := strings.TrimPrefix(request.URL.Path, "/repos/"+repository+"/git/trees/")
			snapshot, ok := byTree[tree]
			if !ok {
				t.Fatalf("unknown tree %s", tree)
			}
			return response(compactAdmissionTreeJSON(snapshot.tree, snapshot.entries)), nil
		case strings.HasPrefix(request.URL.Path, "/repos/"+repository+"/git/blobs/"):
			oid := strings.TrimPrefix(request.URL.Path, "/repos/"+repository+"/git/blobs/")
			raw, ok := byBlob[oid]
			if !ok {
				t.Fatalf("unknown blob %s", oid)
			}
			return response(compactAdmissionBlobJSON(oid, raw)), nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	}))
}
