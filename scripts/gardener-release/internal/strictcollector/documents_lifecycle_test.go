// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import "testing"

const (
	documentTestCommitOID = "1111111111111111111111111111111111111111"
	documentTestTreeOID   = "2222222222222222222222222222222222222222"
	documentTestBlobOID   = "3333333333333333333333333333333333333333"
)

func documentApprovalChild(treeOID string) (*stateV3AssemblyChild, handle, handle) {
	session := &session{
		artifacts:    make(map[string]*artifact),
		assemblyLive: true,
		assemblyRole: stateV3AssemblyMinor,
	}
	rawHandle := handle{session: session, nonce: "raw"}
	treeHandle := handle{session: session, nonce: "tree"}
	session.artifacts[rawHandle.nonce] = &artifact{kind: kindRawCommit, raw: wireRawCommit{SHA: documentTestCommitOID, Tree: wireTreeRef{SHA: documentTestTreeOID}}}
	session.artifacts[treeHandle.nonce] = &artifact{kind: kindTree, tree: wireTree{SHA: treeOID, Tree: []wireTreeEntry{{Path: "active-operation.json", Mode: "100644", Type: "blob", SHA: documentTestBlobOID}}}}
	return &stateV3AssemblyChild{session: session}, rawHandle, treeHandle
}

func TestStateV3DocumentApprovalRequiresCommitBoundTree(t *testing.T) {
	child, raw, tree := documentApprovalChild("4444444444444444444444444444444444444444")
	if _, result := child.approveDocument(raw, tree, stateV3DocumentLease); result.Diagnostic != DiagnosticProtocol {
		t.Fatalf("got %q, want protocol", result.Diagnostic)
	}
}

func TestStateV3DocumentApprovalRecordsAuthenticatedCommit(t *testing.T) {
	child, raw, tree := documentApprovalChild(documentTestTreeOID)
	entry, result := child.approveDocument(raw, tree, stateV3DocumentLease)
	if result.Diagnostic != DiagnosticOK {
		t.Fatal(result)
	}
	if entry.commitOID != documentTestCommitOID || entry.treeOID != documentTestTreeOID {
		t.Fatal("approval did not preserve authenticated commit/tree identity")
	}
}

func TestStateV3DocumentStoreDoesNotInferLaneLifecycleFromInsertionOrder(t *testing.T) {
	store := &stateV3DocumentStore{}
	for _, raw := range [][]byte{[]byte("record-one"), []byte("record-two")} {
		key := documentKey(stateV3DocumentRecord, raw, 'a')
		if !store.putEntry(key, documentEntry(key.kind, "requests/1/2/state.json", stateV3AssemblyMinor, 'a'), raw) {
			t.Fatal("passive cache inferred lifecycle from insertion order")
		}
	}
}

func TestStateV3DocumentStoreRejectsBindingOverflow(t *testing.T) {
	store := &stateV3DocumentStore{bindingCount: stateV3StoreMaxBindings}
	key := documentKey(stateV3DocumentCoordinationArm, []byte("arm"), 'a')
	if store.putEntry(key, documentEntry(key.kind, "mutation-arm.json", stateV3AssemblyCoordination, 'a'), []byte("arm")) {
		t.Fatal("accepted binding overflow")
	}
}

func TestStateV3DocumentStoreBindsReuseToEachCommit(t *testing.T) {
	store := &stateV3DocumentStore{}
	raw := []byte("record")
	key := documentKey(stateV3DocumentRecord, raw, 'a')
	first := documentEntry(key.kind, "requests/1/2/state.json", stateV3AssemblyMinor, 'a')
	second := documentEntry(key.kind, "requests/1/2/state.json", stateV3AssemblyMinor, 'b')
	// Same immutable blob/tree but distinct authenticated snapshot commits need
	// distinct provenance bindings.
	second.treeOID = first.treeOID
	second.admission = testDocumentAdmission(second)
	if !store.putEntry(key, first, raw) || !store.putEntry(key, second, raw) {
		t.Fatal("reused blob was not bound to both commits")
	}
	if store.blobCount != 1 || store.bindingCount != 2 {
		t.Fatalf("got blobs=%d bindings=%d", store.blobCount, store.bindingCount)
	}
}

func TestStateV3DocumentStoreRejectsInvalidBindingAtomically(t *testing.T) {
	store := &stateV3DocumentStore{}
	raw := []byte("bundle")
	key := documentKey(stateV3DocumentBundle, raw, 'a')
	entry := documentEntry(key.kind, "requests/1/2/generation.bundle", stateV3AssemblyMinor, 'a')
	entry.commitOID = "invalid"
	if store.putEntry(key, entry, raw) {
		t.Fatal("accepted invalid commit binding")
	}
	if store.blobCount != 0 || store.bindingCount != 0 || store.rawUsed != 0 {
		t.Fatalf("invalid binding mutated store: blobs=%d bindings=%d raw=%d", store.blobCount, store.bindingCount, store.rawUsed)
	}
}
