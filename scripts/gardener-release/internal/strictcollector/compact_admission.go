// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

// stateV3CompactSnapshot retains only fixed continuation metadata. Semantic
// projections and decoded document bytes are hydrated transiently for one
// adjacent chronological pair and are cleared before the next pair.
type stateV3CompactSnapshot struct {
	commitOID [20]byte
	treeOID   [20]byte
	parentOID [20]byte
	ordinal   uint16
	entries   [5]stateV3CompactDocumentEntry
	count     uint8
}

type stateV3CompactDocumentEntry struct {
	oid   [20]byte
	index uint16
	kind  stateV3DocumentKind
	path  [stateV3StoredPathBytes]byte
	len   uint8
}

// stateV3CompactLaneHistory owns no decoded HTTP artifact, semantic
// projection, raw document bytes, slices, strings, maps, or pointers.
type stateV3CompactLaneHistory struct {
	snapshots [gardenerrelease.MaxStateV3StateLaneHistoryCommits]stateV3CompactSnapshot
	count     uint16
}

func (h *stateV3CompactLaneHistory) append(raw wireRawCommit, tree wireTree, role stateV3AssemblyRole) bool {
	if h == nil || h.count == gardenerrelease.MaxStateV3StateLaneHistoryCommits || role != stateV3AssemblyMinor && role != stateV3AssemblyPatch {
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
	entries, ok := stateV3DiscoverDocumentEntries(role, tree.Tree)
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
	h.count++
	return true
}

// compactEntry derives every entry field from the fixed slot owned by snapshot.
// No entry-valued caller input can select a blob request or admission token.
func compactEntry(snapshot *stateV3CompactSnapshot, document int, role stateV3AssemblyRole) (stateV3ApprovedDocumentEntry, bool) {
	if snapshot == nil || document < 0 || document >= int(snapshot.count) || role != stateV3AssemblyMinor && role != stateV3AssemblyPatch {
		return stateV3ApprovedDocumentEntry{}, false
	}
	compact := snapshot.entries[document]
	path := string(compact.path[:compact.len])
	if !stateV3DocumentPathAllowed(role, compact.kind, path) {
		return stateV3ApprovedDocumentEntry{}, false
	}
	return stateV3ApprovedDocumentEntry{
		index: int(compact.index), kind: compact.kind, path: path, oid: hexFixedOID(compact.oid),
		commitOID: hexFixedOID(snapshot.commitOID), treeOID: hexFixedOID(snapshot.treeOID), role: role,
	}, true
}

// compactEntry returns an entry value copied from session-owned authenticated
// compact history. It deliberately returns neither a snapshot pointer nor any
// reference into session state, so callers cannot alter blob-read authority.
func (s *session) compactEntry(generation uint64, ordinal uint16, document int) (stateV3ApprovedDocumentEntry, bool) {
	if s == nil {
		return stateV3ApprovedDocumentEntry{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.assemblyLive || s.compactHistory == nil || s.compactGeneration != generation || int(ordinal) >= int(s.compactHistory.count) {
		return stateV3ApprovedDocumentEntry{}, false
	}
	snapshot := &s.compactHistory.snapshots[ordinal]
	if snapshot.ordinal != ordinal {
		return stateV3ApprovedDocumentEntry{}, false
	}
	return compactEntry(snapshot, document, s.assemblyRole)
}

func (s *session) readAssemblyCompactBlob(generation uint64, ordinal uint16, document int) (handle, Result) {
	entry, ok := s.compactEntry(generation, ordinal, document)
	if !ok {
		return handle{}, failure(DiagnosticProtocol)
	}
	return s.settleFixed(fixedRequest{scope: fixedAssembly, kind: kindBlob, purpose: fixedRequestBlob, oid: entry.oid})
}

// collectStateV3LaneDocumentsForPolicy derives every lane selector from the
// active child role and the reviewed policy. No caller can select a ref,
// checkpoint, or history length for compact document admission.
// collectStateV3LaneDocuments authenticates the complete role-bound spine
// before hydration or blob dispatch. Checkpoint and history maximum are always
// derived from the active fixed role and policy.
func (c *stateV3AssemblyChild) collectStateV3LaneDocuments(policy gardenerrelease.StateV3Policy) Result {
	if c == nil || c.session == nil || c.store == nil || c.session.assemblyRole != stateV3AssemblyMinor && c.session.assemblyRole != stateV3AssemblyPatch {
		return failure(DiagnosticProtocol)
	}
	var checkpoint string
	var maximum int
	switch c.session.assemblyRole {
	case stateV3AssemblyMinor:
		checkpoint, maximum = policy.StateLanes.Minor.CheckpointOID, policy.StateLanes.Minor.MaxHistoryCommits
	case stateV3AssemblyPatch:
		checkpoint, maximum = policy.StateLanes.Patch.CheckpointOID, policy.StateLanes.Patch.MaxHistoryCommits
	}
	if !validOID(checkpoint) || maximum <= 0 || maximum > gardenerrelease.MaxStateV3StateLaneHistoryCommits {
		return failure(DiagnosticProtocol)
	}
	var history stateV3CompactLaneHistory
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
		raw, ok := c.session.rawCommitFor(current)
		if !ok {
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
		// This projection authenticates one received snapshot and is discarded
		// immediately. It is never retained in compact history.
		transientBudget := stateV3SpineBudget{}
		_, evidenceOK := stateV3SnapshotEvidence(raw, rest, gql, tree, policy, raw.SHA == checkpoint, &transientBudget)
		if !restOK || !gqlOK || !treeOK || !evidenceOK || !history.append(raw, tree, c.session.assemblyRole) {
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
	if !checkpointReached || !historyReachedCheckpoint(&history, checkpoint) || history.snapshots[history.count-1].count != 0 {
		c.store.reset()
		return failure(DiagnosticRequiredEvidenceAbsent)
	}
	admission := &stateV3LaneHistoryAdmission{child: c, generation: 1}
	c.session.mu.Lock()
	if c.session.compactHistory != nil {
		c.session.mu.Unlock()
		c.store.reset()
		return failure(DiagnosticProtocol)
	}
	c.session.compactHistory, c.session.compactGeneration = &history, admission.generation
	c.session.mu.Unlock()
	defer func() {
		admission.close()
		c.session.mu.Lock()
		if c.session.compactHistory == &history && c.session.compactGeneration == admission.generation {
			c.session.compactHistory = nil
			c.session.compactGeneration++
		}
		c.session.mu.Unlock()
	}()
	parent, result := c.loadCompactSnapshot(admission.generation, uint16(history.count-1), policy, true)
	if result.Diagnostic != DiagnosticOK {
		c.store.reset()
		return result
	}
	defer parent.clear()
	for child := int(history.count) - 2; child >= 0; child-- {
		current, loadResult := c.loadCompactSnapshot(admission.generation, uint16(child), policy, false)
		if loadResult.Diagnostic != DiagnosticOK {
			parent.clear()
			c.store.reset()
			return loadResult
		}
		changes, err := gardenerrelease.DeriveStateV3TreeChanges(parent.snapshot.Tree, current.snapshot.Tree)
		if err != nil {
			current.clear()
			parent.clear()
			c.store.reset()
			return failure(DiagnosticResponseInvalid)
		}
		current.snapshot.Commit.ChangedPaths = changes
		_, ok := admission.classifyAdjacentSnapshot(
			stateV3AuthenticatedSnapshot{ordinal: history.snapshots[child+1].ordinal, projected: parent.snapshot},
			stateV3AuthenticatedSnapshot{ordinal: history.snapshots[child].ordinal, projected: current.snapshot}, policy, child+1 == int(history.count)-1)
		if !ok {
			current.clear()
			parent.clear()
			c.store.reset()
			return failure(DiagnosticResponseInvalid)
		}
		for document := 0; document < int(current.count); document++ {
			entry, entryOK := compactEntry(&history.snapshots[child], document, c.session.assemblyRole)
			token, admitted := admission.admitCompact(uint16(child), document)
			if !entryOK || !admitted {
				current.clear()
				parent.clear()
				c.store.reset()
				return failure(DiagnosticProtocol)
			}
			entry.admission = token
			raw := current.documents[document].raw
			digest := compactDocumentDigest(raw)
			key := stateV3DocumentKey{oid: entry.oid, digest: hex.EncodeToString(digest[:]), kind: entry.kind}
			if !c.store.putEntry(key, entry, raw) {
				current.clear()
				parent.clear()
				c.store.reset()
				return failure(DiagnosticProtocol)
			}
		}
		parent.clear()
		parent = current
	}
	return Result{}
}

func historyReachedCheckpoint(history *stateV3CompactLaneHistory, checkpoint string) bool {
	if history == nil || history.count == 0 {
		return false
	}
	checkpointOID, ok := decodeFixedOID(checkpoint)
	return ok && history.snapshots[history.count-1].commitOID == checkpointOID
}

type stateV3LoadedCompactDocument struct{ raw []byte }
type stateV3LoadedCompactSnapshot struct {
	snapshot  gardenerrelease.StateV3StateSnapshot
	documents [5]stateV3LoadedCompactDocument
	count     uint8
}

func (l *stateV3LoadedCompactSnapshot) clear() {
	if l == nil {
		return
	}
	for i := 0; i < int(l.count); i++ {
		clear(l.documents[i].raw)
		l.documents[i].raw = nil
	}
	*l = stateV3LoadedCompactSnapshot{}
}

// loadCompactSnapshot creates a transient parent semantic projection. Its
// document bytes live only until this snapshot ceases to be one side of the
// adjacent transition; store.putEntry owns the only retained document copy.
func (c *stateV3AssemblyChild) loadCompactSnapshot(generation uint64, ordinal uint16, policy gardenerrelease.StateV3Policy, checkpoint bool) (stateV3LoadedCompactSnapshot, Result) {
	if c == nil || c.session == nil {
		return stateV3LoadedCompactSnapshot{}, failure(DiagnosticProtocol)
	}
	c.session.mu.Lock()
	history := c.session.compactHistory
	live := c.session.assemblyLive && c.session.compactGeneration == generation
	c.session.mu.Unlock()
	if !live || history == nil || int(ordinal) >= int(history.count) {
		return stateV3LoadedCompactSnapshot{}, failure(DiagnosticProtocol)
	}
	compact := &history.snapshots[ordinal]
	if _, ok := c.session.compactEntry(generation, ordinal, 0); !ok && compact.count != 0 {
		return stateV3LoadedCompactSnapshot{}, failure(DiagnosticProtocol)
	}
	loaded := stateV3LoadedCompactSnapshot{snapshot: compactProjection(compact, policy, checkpoint)}
	if loaded.snapshot.Commit.OID == "" {
		return stateV3LoadedCompactSnapshot{}, failure(DiagnosticProtocol)
	}
	for i := 0; i < int(compact.count); i++ {
		entry, ok := compactEntry(compact, i, c.session.assemblyRole)
		if !ok {
			loaded.clear()
			return stateV3LoadedCompactSnapshot{}, failure(DiagnosticProtocol)
		}
		blobHandle, result := c.session.readAssemblyCompactBlob(generation, ordinal, i)
		if result.Diagnostic != DiagnosticOK {
			loaded.clear()
			return stateV3LoadedCompactSnapshot{}, result
		}
		blob, blobOK := c.session.blobFor(blobHandle)
		c.session.Release(blobHandle)
		if !blobOK || blob.SHA != entry.oid || !stateV3DocumentValid(entry.kind, entry.path, blob.Raw) {
			loaded.clear()
			return stateV3LoadedCompactSnapshot{}, failure(DiagnosticResponseInvalid)
		}
		loaded.documents[i].raw = append([]byte(nil), blob.Raw...)
		loaded.count++
	}
	documents := make([]stateV3LoadedCompactDocument, loaded.count)
	copy(documents, loaded.documents[:loaded.count])
	if !hydrateCompactSnapshot(&loaded.snapshot, compact, documents, c.session.assemblyRole) {
		loaded.clear()
		return stateV3LoadedCompactSnapshot{}, failure(DiagnosticResponseInvalid)
	}
	return loaded, Result{}
}

func compactProjection(compact *stateV3CompactSnapshot, policy gardenerrelease.StateV3Policy, checkpoint bool) gardenerrelease.StateV3StateSnapshot {
	if compact == nil {
		return gardenerrelease.StateV3StateSnapshot{}
	}
	entries := make([]gardenerrelease.StateV3StateTreeEntry, 0, compact.count)
	for i := 0; i < int(compact.count); i++ {
		entry := compact.entries[i]
		entries = append(entries, gardenerrelease.StateV3StateTreeEntry{Path: string(entry.path[:entry.len]), Mode: "100644", Type: "blob", OID: hexFixedOID(entry.oid)})
	}
	parentOID := ""
	if !checkpoint {
		parentOID = hexFixedOID(compact.parentOID)
	}
	return gardenerrelease.StateV3StateSnapshot{
		Commit: gardenerrelease.StateV3StateCommitEvidence{OID: hexFixedOID(compact.commitOID), TreeOID: hexFixedOID(compact.treeOID), ParentOID: parentOID, RESTVerified: true, RESTReason: gardenerrelease.StateV3RequiredRESTVerificationReason, GraphQLSignatureValid: true, WasSignedByGitHub: true, SignatureState: gardenerrelease.StateV3RequiredSignatureState, Roles: policy.CommitRoles},
		Tree:   gardenerrelease.StateV3StateTreeEvidence{OID: hexFixedOID(compact.treeOID), Complete: true, Entries: entries},
	}
}

func hydrateCompactSnapshot(snapshot *gardenerrelease.StateV3StateSnapshot, compact *stateV3CompactSnapshot, documents []stateV3LoadedCompactDocument, role stateV3AssemblyRole) bool {
	if snapshot == nil || compact == nil || len(documents) != int(compact.count) {
		return false
	}
	var record gardenerrelease.StateV3Record
	var recordSet bool
	for i, document := range documents {
		entry, ok := compactEntry(compact, i, role)
		if !ok {
			return false
		}
		digest := compactDocumentDigest(document.raw)
		switch entry.kind {
		case stateV3DocumentLease:
			var lease gardenerrelease.StateV3ActiveOperationLease
			if !compactDocumentJSON(document.raw, &lease) {
				return false
			}
			snapshot.LeasePresent, snapshot.LeasePath, snapshot.RawLease, snapshot.LeaseSHA256, snapshot.LeaseBlobOID = true, entry.path, document.raw, hex.EncodeToString(digest[:]), entry.oid
		case stateV3DocumentRecord:
			value, err := gardenerrelease.DecodeStateV3Record(document.raw)
			if err != nil {
				return false
			}
			record, recordSet = value, true
			snapshot.RecordPresent, snapshot.RecordPath, snapshot.RawRecord, snapshot.RecordSHA256, snapshot.RecordBlobOID = true, entry.path, document.raw, hex.EncodeToString(digest[:]), entry.oid
			snapshot.ActiveRecord = gardenerrelease.StateV3ActiveRecordEvidence{Present: true, Path: entry.path, Raw: document.raw, SHA256: hex.EncodeToString(digest[:]), BlobOID: entry.oid}
		}
	}
	if !recordSet {
		return !snapshot.RecordPresent && !snapshot.LeasePresent || snapshot.LeasePresent
	}
	bundleIndex := documentKindIndex(compact, stateV3DocumentBundle)
	preparedIndex := documentKindIndex(compact, stateV3DocumentPrepared)
	reservationIndex := documentKindIndex(compact, stateV3DocumentReservation)
	hasEnvelope := bundleIndex >= 0 || preparedIndex >= 0 || reservationIndex >= 0
	if hasEnvelope {
		if bundleIndex < 0 || preparedIndex < 0 || reservationIndex < 0 || record.Phase != gardenerrelease.StateV3PhaseReserved && record.Prepared == nil {
			return false
		}
		bundle, prepared, reservation := documents[bundleIndex], documents[preparedIndex], documents[reservationIndex]
		bundleEntry, bundleOK := compactEntry(compact, bundleIndex, role)
		preparedEntry, preparedOK := compactEntry(compact, preparedIndex, role)
		reservationEntry, reservationOK := compactEntry(compact, reservationIndex, role)
		manifestPrepared, manifestPlans, manifestOK := gardenerrelease.DecodeStateV3PreparedManifestDocument(prepared.raw)
		if !bundleOK || !preparedOK || !reservationOK || !manifestOK {
			return false
		}
		bundleDigest, preparedDigest, reservationDigest := compactDocumentDigest(bundle.raw), compactDocumentDigest(prepared.raw), compactDocumentDigest(reservation.raw)
		files := []gardenerrelease.StateV3StagedFile{{Path: bundleEntry.path, Raw: bundle.raw, SHA256: hex.EncodeToString(bundleDigest[:]), BlobOID: bundleEntry.oid, SizeBytes: int64(len(bundle.raw))}, {Path: preparedEntry.path, Raw: prepared.raw, SHA256: hex.EncodeToString(preparedDigest[:]), BlobOID: preparedEntry.oid, SizeBytes: int64(len(prepared.raw))}, {Path: reservationEntry.path, Raw: reservation.raw, SHA256: hex.EncodeToString(reservationDigest[:]), BlobOID: reservationEntry.oid, SizeBytes: int64(len(reservation.raw))}}
		if manifestPrepared.Bundle.Path != files[0].Path || manifestPrepared.Bundle.SHA256 != files[0].SHA256 || manifestPrepared.Bundle.BlobOID != files[0].BlobOID || manifestPrepared.Bundle.SizeBytes != files[0].SizeBytes {
			return false
		}
		manifestPrepared.StateFiles = []gardenerrelease.StateV3PreparedFile{{Path: files[0].Path, SHA256: files[0].SHA256, BlobOID: files[0].BlobOID, SizeBytes: files[0].SizeBytes}, {Path: files[1].Path, SHA256: files[1].SHA256, BlobOID: files[1].BlobOID, SizeBytes: files[1].SizeBytes}, {Path: files[2].Path, SHA256: files[2].SHA256, BlobOID: files[2].BlobOID, SizeBytes: files[2].SizeBytes}}
		snapshot.StagedEnvelope = &gardenerrelease.StateV3StagedEnvelopeEvidence{Prepared: manifestPrepared, TagPlans: manifestPlans, Files: files}
		snapshot.ActiveRecord.StagedEnvelope = snapshot.StagedEnvelope
	}
	return true
}

func documentKindIndex(compact *stateV3CompactSnapshot, kind stateV3DocumentKind) int {
	if compact == nil {
		return -1
	}
	for i := 0; i < int(compact.count); i++ {
		if compact.entries[i].kind == kind {
			return i
		}
	}
	return -1
}

func mustFixedOID(value string) [20]byte             { result, _ := decodeFixedOID(value); return result }
func compactDocumentJSON(raw []byte, value any) bool { return json.Unmarshal(raw, value) == nil }
func compactDocumentDigest(raw []byte) [32]byte      { return sha256.Sum256(raw) }
