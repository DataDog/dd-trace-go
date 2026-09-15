// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"crypto/sha256"
	"encoding/hex"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

// stateV3CompactCoordinationHistory contains only authenticated fixed commit,
// tree, parent, and approved-document metadata. It has its own 512-entry
// capacity so state-lane compact histories retain their tighter 455-entry
// allocation topology.
type stateV3CompactCoordinationHistory struct {
	snapshots [gardenerrelease.MaxStateV3CoordinationHistoryCommits]stateV3CompactSnapshot
	count     uint16
}

func (h *stateV3CompactCoordinationHistory) append(raw wireRawCommit, tree wireTree) bool {
	if h == nil || h.count == gardenerrelease.MaxStateV3CoordinationHistoryCommits {
		return false
	}
	commitOID, commitOK := decodeFixedOID(raw.SHA)
	treeOID, treeOK := decodeFixedOID(tree.SHA)
	if !commitOK || !treeOK || raw.Tree.SHA != tree.SHA || len(raw.Parents) > 1 {
		return false
	}
	var parentOID [20]byte
	if len(raw.Parents) == 1 {
		var parentOK bool
		parentOID, parentOK = decodeFixedOID(raw.Parents[0].SHA)
		if !parentOK {
			return false
		}
	}
	entries, ok := stateV3DiscoverDocumentEntries(stateV3AssemblyCoordination, tree.Tree)
	if !ok || len(entries) > len(h.snapshots[0].entries) {
		return false
	}
	snapshot := &h.snapshots[h.count]
	snapshot.commitOID, snapshot.treeOID, snapshot.parentOID, snapshot.ordinal = commitOID, treeOID, parentOID, h.count
	for i := range entries {
		entry := entries[i]
		oid, oidOK := decodeFixedOID(entry.oid)
		if !oidOK || len(entry.path) > stateV3StoredPathBytes || entry.index < 0 || entry.index > int(^uint16(0)) {
			return false
		}
		snapshot.entries[i] = stateV3CompactDocumentEntry{oid: oid, index: uint16(entry.index), kind: entry.kind, len: uint8(len(entry.path))}
		copy(snapshot.entries[i].path[:], entry.path)
	}
	snapshot.count = uint8(len(entries))
	snapshot.treeEmpty = len(tree.Tree) == 0
	h.count++
	return true
}

func coordinationHistoryReachedCheckpoint(history *stateV3CompactCoordinationHistory, checkpoint string) bool {
	if history == nil || history.count == 0 {
		return false
	}
	value, ok := decodeFixedOID(checkpoint)
	return ok && history.snapshots[history.count-1].commitOID == value
}

// collectStateV3CoordinationDocuments authenticates and chronologically admits
// the sole coordination history. It uses only its fixed assembly role, the
// policy checkpoint, and tree-derived document slots. It intentionally does
// not construct a coordination authorization result: Git history does not
// contain the independently reread mutation outcomes required by the parent
// coordination validator.
func (c *stateV3AssemblyChild) collectStateV3CoordinationDocuments() Result {
	if c == nil || c.session == nil || c.store == nil || c.policy == nil || c.session.assemblyRole != stateV3AssemblyCoordination {
		return failure(DiagnosticProtocol)
	}
	policy := c.policy.value
	checkpoint, maximum := policy.Coordination.CheckpointOID, policy.Coordination.MaxHistoryCommits
	if !validOID(checkpoint) || maximum <= 0 || maximum > gardenerrelease.MaxStateV3CoordinationHistoryCommits {
		return failure(DiagnosticProtocol)
	}
	var history stateV3CompactCoordinationHistory
	root, result := c.readRoot()
	if result.Diagnostic != DiagnosticOK {
		return result
	}
	current, result := c.readRawCommitForRoot(root)
	c.session.Release(root)
	if result.Diagnostic != DiagnosticOK {
		return result
	}
	checkpointReached := false
	for int(history.count) < maximum {
		raw, rawOK := c.session.rawCommitFor(current)
		if !rawOK {
			c.session.Release(current)
			c.store.reset()
			return failure(DiagnosticProtocol)
		}
		for i := 0; i < int(history.count); i++ {
			if history.snapshots[i].commitOID == mustFixedOID(raw.SHA) {
				c.session.Release(current)
				c.store.reset()
				return failure(DiagnosticConflict)
			}
		}
		restHandle, restResult := c.readRESTCommit(current)
		if restResult.Diagnostic != DiagnosticOK {
			c.session.Release(current)
			c.store.reset()
			return restResult
		}
		rest, restOK := c.session.restCommitFor(restHandle)
		c.session.Release(restHandle)
		gqlHandle, gqlResult := c.readGraphQLCommit(current)
		if gqlResult.Diagnostic != DiagnosticOK {
			c.session.Release(current)
			c.store.reset()
			return gqlResult
		}
		gql, gqlOK := c.session.graphQLCommitFor(gqlHandle)
		c.session.Release(gqlHandle)
		treeHandle, treeResult := c.readTree(current)
		if treeResult.Diagnostic != DiagnosticOK {
			c.session.Release(current)
			c.store.reset()
			return treeResult
		}
		tree, treeOK := c.session.treeFor(treeHandle)
		c.session.Release(treeHandle)
		budget := stateV3SpineBudget{}
		_, evidenceOK := stateV3SnapshotEvidence(raw, rest, gql, tree, policy, raw.SHA == checkpoint, &budget)
		if !restOK || !gqlOK || !treeOK || !evidenceOK || !history.append(raw, tree) {
			c.session.Release(current)
			c.store.reset()
			return failure(DiagnosticConflict)
		}
		if raw.SHA == checkpoint {
			checkpointReached = true
			c.session.Release(current)
			break
		}
		if len(raw.Parents) != 1 {
			c.session.Release(current)
			c.store.reset()
			return failure(DiagnosticRequiredEvidenceAbsent)
		}
		parent, parentResult := c.readRawCommitParent(current)
		c.session.Release(current)
		if parentResult.Diagnostic != DiagnosticOK {
			c.store.reset()
			return parentResult
		}
		current = parent
	}
	if !checkpointReached || !coordinationHistoryReachedCheckpoint(&history, checkpoint) || !history.snapshots[history.count-1].treeEmpty {
		c.store.reset()
		return failure(DiagnosticRequiredEvidenceAbsent)
	}
	admission := &stateV3LaneHistoryAdmission{child: c, generation: 1}
	c.session.mu.Lock()
	if c.session.coordinationCompactHistory != nil || c.session.compactHistory != nil {
		c.session.mu.Unlock()
		c.store.reset()
		return failure(DiagnosticProtocol)
	}
	c.session.coordinationCompactHistory, c.session.compactGeneration = &history, admission.generation
	c.session.mu.Unlock()
	defer func() {
		admission.close()
		c.session.mu.Lock()
		if c.session.coordinationCompactHistory == &history && c.session.compactGeneration == admission.generation {
			c.session.coordinationCompactHistory = nil
			c.session.compactGeneration++
		}
		c.session.mu.Unlock()
	}()
	// Documents are retained only after the complete raw/REST/GraphQL/tree
	// spine reaches its exact empty checkpoint. No path/OID is caller supplied.
	for ordinal := int(history.count) - 1; ordinal >= 0; ordinal-- {
		for document := 0; document < int(history.snapshots[ordinal].count); document++ {
			entry, entryOK := c.session.compactEntry(admission.generation, uint16(ordinal), document)
			token, admitted := admission.admitCompact(uint16(ordinal), document)
			if !entryOK || !admitted {
				c.store.reset()
				return failure(DiagnosticProtocol)
			}
			blobHandle, blobResult := c.session.readAssemblyCompactBlob(admission.generation, uint16(ordinal), document)
			if blobResult.Diagnostic != DiagnosticOK {
				c.store.reset()
				return blobResult
			}
			blob, blobOK := c.session.blobFor(blobHandle)
			c.session.Release(blobHandle)
			if !blobOK || blob.SHA != entry.oid || !stateV3DocumentValid(entry.kind, entry.path, blob.Raw) {
				c.store.reset()
				return failure(DiagnosticResponseInvalid)
			}
			digest := sha256.Sum256(blob.Raw)
			entry.admission = token
			key := stateV3DocumentKey{oid: entry.oid, digest: hex.EncodeToString(digest[:]), kind: entry.kind}
			if !c.store.putEntry(key, entry, blob.Raw) {
				c.store.reset()
				return failure(DiagnosticProtocol)
			}
		}
	}
	return Result{}
}
