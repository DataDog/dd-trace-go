// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"encoding/json"
	"testing"
)

func baseReservation() Reservation {
	return Reservation{
		SchemaVersion:            "1",
		RequestKey:               "123:789",
		RequestSHA256:            "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		RepositoryID:             "123",
		RepositoryFullName:       RepositoryFullName,
		IssueNumber:              "456",
		OriginalCommentID:        "789",
		AcknowledgementCommentID: "790",
		Command:                  "release:promote",
		RequestedVersion:         "auto",
		BodySnapshot:             "/gardener release:promote",
		ValidatedActorID:         "1001",
		ValidatedActorLogin:      "octo-releaser",
		PolicyRevision:           "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		ResolvedVersion:          "v2.11.0-rc.1",
		ReleaseLine:              "v2.11",
		CreatedAt:                "2026-01-01T00:00:00Z",
	}
}

func TestStatePathsBuildsOnlyFromValidatedDecimalIDs(t *testing.T) {
	paths, err := StatePaths("123", "789")
	if err != nil {
		t.Fatal(err)
	}
	if paths.Reservation != "requests/123/789/reservation.json" {
		t.Fatalf("reservation path = %q", paths.Reservation)
	}
	if paths.EventPath(1) != "requests/123/789/events/000001.json" {
		t.Fatalf("event path = %q", paths.EventPath(1))
	}
	if paths.Signed != "requests/123/789/signed.json" || paths.RecoveryBundle != "requests/123/789/recovery.bundle" {
		t.Fatalf("unexpected fixed paths: %#v", paths)
	}
}

func TestStatePathsRejectsUnsafeIDs(t *testing.T) {
	cases := []struct{ repo, comment string }{
		{"0123", "789"},
		{"123", "0789"},
		{"123;echo pwned", "789"},
		{"../../etc", "789"},
		{"", "789"},
	}
	for _, tc := range cases {
		if _, err := StatePaths(tc.repo, tc.comment); ErrorCode(err) != "unsafe_id" {
			t.Fatalf("StatePaths(%q, %q) error = %q, want unsafe_id", tc.repo, tc.comment, ErrorCode(err))
		}
	}
}

func TestAppendEventAndVerifyEventChain(t *testing.T) {
	events, err := AppendEvent(nil, "123:789", EventReserved, json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	events, err = AppendEvent(events, "123:789", EventPhaseAdvanced, json.RawMessage(`{"phase":"signed"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 1 || events[1].Sequence != 2 {
		t.Fatalf("unexpected events: %#v", events)
	}
	if events[0].PreviousDigest != "" {
		t.Fatalf("first event previous digest = %q, want empty", events[0].PreviousDigest)
	}
	if events[1].PreviousDigest != events[0].Digest {
		t.Fatalf("second event previous digest = %q, want %q", events[1].PreviousDigest, events[0].Digest)
	}
	if err := VerifyEventChain("123:789", events); err != nil {
		t.Fatalf("verify chain: %v", err)
	}
}

func TestVerifyEventChainDetectsReorderTamperAndWrongKey(t *testing.T) {
	events, err := AppendEvent(nil, "123:789", EventReserved, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	events, err = AppendEvent(events, "123:789", EventPhaseAdvanced, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("reordered", func(t *testing.T) {
		reordered := []Event{events[1], events[0]}
		reordered[0].Sequence, reordered[1].Sequence = 1, 2
		if err := VerifyEventChain("123:789", reordered); ErrorCode(err) != "event_chain_broken" {
			t.Fatalf("error = %q, want event_chain_broken", ErrorCode(err))
		}
	})

	t.Run("tampered evidence", func(t *testing.T) {
		tampered := append([]Event(nil), events...)
		tampered[0].Evidence = json.RawMessage(`{"tampered":true}`)
		if err := VerifyEventChain("123:789", tampered); ErrorCode(err) != "event_digest_mismatch" {
			t.Fatalf("error = %q, want event_digest_mismatch", ErrorCode(err))
		}
	})

	t.Run("wrong request key", func(t *testing.T) {
		if err := VerifyEventChain("999:999", events); ErrorCode(err) != "event_request_key_mismatch" {
			t.Fatalf("error = %q, want event_request_key_mismatch", ErrorCode(err))
		}
	})

	t.Run("sequence gap", func(t *testing.T) {
		gapped := append([]Event(nil), events...)
		gapped[1].Sequence = 3
		if err := VerifyEventChain("123:789", gapped); ErrorCode(err) != "event_sequence_gap" {
			t.Fatalf("error = %q, want event_sequence_gap", ErrorCode(err))
		}
	})
}

// TestReserveOperationS03RejectsDifferentBodyHash covers §15 S03: the same
// request ID with a different body hash is a conflict, and the caller must
// be told to preserve the old record rather than overwrite it.
func TestReserveOperationS03RejectsDifferentBodyHash(t *testing.T) {
	existing := Record{Reservation: baseReservation(), Phase: PhaseReserved}
	incoming := baseReservation()
	incoming.RequestSHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	incoming.BodySnapshot = "/gardener release:promote v9.9"
	_, err := ReserveOperation(&existing, incoming, nil)
	if ErrorCode(err) != "immutable_field_changed" || ClassOf(err) != ErrorClassStateConflict {
		t.Fatalf("error = %q/%q, want state_conflict immutable_field_changed", ClassOf(err), ErrorCode(err))
	}
}

// TestReserveOperationRejectsDifferingSourceRefs proves source_refs[] is
// enforced as part of §13.5's immutable reservation: a retry that changes
// which source commit the request resolved against is a conflict, even
// when every other immutable field (including the request hash) matches.
func TestReserveOperationRejectsDifferingSourceRefs(t *testing.T) {
	existing := Record{Reservation: baseReservation(), Phase: PhaseReserved}
	existing.Reservation.SourceRefs = []SourceRef{{Ref: "refs/heads/main", SHA: "0123456789abcdef0123456789abcdef01234567"}}
	incoming := existing.Reservation
	incoming.SourceRefs = []SourceRef{{Ref: "refs/heads/main", SHA: "ffffffffffffffffffffffffffffffffffffffff"}}
	_, err := ReserveOperation(&existing, incoming, nil)
	if ErrorCode(err) != "immutable_field_changed" || ClassOf(err) != ErrorClassStateConflict {
		t.Fatalf("error = %q/%q, want state_conflict immutable_field_changed", ClassOf(err), ErrorCode(err))
	}
}

// TestReserveOperationS04TreatsIdenticalRetryAsSuccess covers §15 S04: a
// record write that already succeeded, replayed with byte-identical
// immutable fields, must be recognized as the same operation rather than
// treated as a new reservation attempt or a conflict.
func TestReserveOperationS04TreatsIdenticalRetryAsSuccess(t *testing.T) {
	reservation := baseReservation()
	existing := Record{Reservation: reservation, Phase: PhaseSigned, WorkflowSHA: "abc"}
	decision, err := ReserveOperation(&existing, reservation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Reserved || decision.Record.Phase != PhaseSigned || decision.Record.WorkflowSHA != "abc" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

// TestReserveOperationNewRequestAppendsReservedEvent proves a brand new
// request key with no conflicting same-line operation is accepted and
// gets a single "reserved" event.
func TestReserveOperationNewRequestAppendsReservedEvent(t *testing.T) {
	incoming := baseReservation()
	decision, err := ReserveOperation(nil, incoming, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Reserved || decision.Record.Phase != PhaseReserved || len(decision.Record.Events) != 1 {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Record.Events[0].Kind != EventReserved {
		t.Fatalf("event kind = %q, want reserved", decision.Record.Events[0].Kind)
	}
}

// TestReserveOperationV04BlocksIncompleteOperationOnSameReleaseLine
// covers §15's V04-style requirement: an incomplete prior operation on the
// same release line blocks a new request rather than guessing a repair,
// even though the new request's own reservation fields are otherwise
// valid in isolation.
func TestReserveOperationV04BlocksIncompleteOperationOnSameReleaseLine(t *testing.T) {
	other := baseReservation()
	other.RequestKey = "123:999"
	other.OriginalCommentID = "999"
	incompleteSameLine := []Record{{Reservation: other, Phase: PhaseBranchesPublished}}

	incoming := baseReservation()
	incoming.RequestKey = "123:111"
	incoming.OriginalCommentID = "111"
	_, err := ReserveOperation(nil, incoming, incompleteSameLine)
	if ErrorCode(err) != "incomplete_operation_on_release_line" || ClassOf(err) != ErrorClassStateConflict {
		t.Fatalf("error = %q/%q, want state_conflict incomplete_operation_on_release_line", ClassOf(err), ErrorCode(err))
	}
}

// TestReserveOperationAllowsNewRequestWhenSameLineListIsEmpty proves that
// a new request on a release line with no *currently incomplete*
// operations is accepted. Filtering out complete operations is
// IncompleteOperationsOnLine's contract (proved in state_git_test.go);
// this test proves ReserveOperation's own behavior once that filtering
// has already happened, i.e. §13.5's phase table where `complete` frees
// the line for a new operation.
func TestReserveOperationAllowsNewRequestWhenSameLineListIsEmpty(t *testing.T) {
	incoming := baseReservation()
	incoming.RequestKey = "123:111"
	incoming.OriginalCommentID = "111"
	decision, err := ReserveOperation(nil, incoming, []Record{})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Reserved {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestKnownPhaseAndAdvancePhase(t *testing.T) {
	if KnownPhase("bogus") {
		t.Fatal("KnownPhase accepted unknown phase")
	}
	if !KnownPhase(PhaseReserved) {
		t.Fatal("KnownPhase rejected reserved")
	}
	next, err := AdvancePhase(PhaseReserved, PhaseSigned)
	if err != nil || next != PhaseSigned {
		t.Fatalf("AdvancePhase(reserved, signed) = %q, %v", next, err)
	}
	if _, err := AdvancePhase(PhaseSigned, PhaseReserved); ErrorCode(err) != "phase_regression" {
		t.Fatalf("error = %q, want phase_regression", ErrorCode(err))
	}
	if _, err := AdvancePhase(PhaseReserved, PhaseReserved); ErrorCode(err) != "phase_regression" {
		t.Fatalf("error = %q, want phase_regression for same-phase transition", ErrorCode(err))
	}
	if _, err := AdvancePhase("bogus", PhaseSigned); ErrorCode(err) != "unknown_operation_phase" {
		t.Fatalf("error = %q, want unknown_operation_phase", ErrorCode(err))
	}
}

// TestReconcileAcknowledgementA08KeepsCanonicalIDAcrossVerifiedDuplicates
// covers §15's A06/A08-adjacent requirement: once a record has a stored
// canonical acknowledgement ID, a differing but authentic acknowledgement
// for the same request (matching marker) reconciles to the *stored* ID.
// It never rewrites the canonical ID, even though the candidate is itself
// verified/authentic.
func TestReconcileAcknowledgementA08KeepsCanonicalIDAcrossVerifiedDuplicates(t *testing.T) {
	reservation := baseReservation()
	marker := Marker(reservation.RepositoryID, reservation.OriginalCommentID, reservation.Command, reservation.ResolvedVersion)
	record := Record{Reservation: reservation}

	id, err := ReconcileAcknowledgement(record, AcknowledgementCandidate{CommentID: "999", Marker: marker}, marker)
	if err != nil {
		t.Fatal(err)
	}
	if id != reservation.AcknowledgementCommentID {
		t.Fatalf("reconciled ID = %q, want stored canonical %q", id, reservation.AcknowledgementCommentID)
	}
}

// TestReconcileAcknowledgementAdoptsFirstCandidateWhenUnset covers the
// first-reservation path: an empty stored ID adopts the first verified
// candidate as canonical.
func TestReconcileAcknowledgementAdoptsFirstCandidateWhenUnset(t *testing.T) {
	reservation := baseReservation()
	reservation.AcknowledgementCommentID = ""
	marker := Marker(reservation.RepositoryID, reservation.OriginalCommentID, reservation.Command, reservation.ResolvedVersion)
	record := Record{Reservation: reservation}

	id, err := ReconcileAcknowledgement(record, AcknowledgementCandidate{CommentID: "790", Marker: marker}, marker)
	if err != nil {
		t.Fatal(err)
	}
	if id != "790" {
		t.Fatalf("reconciled ID = %q, want 790", id)
	}
}

// TestReconcileAcknowledgementRejectsMismatchedMarker proves an
// acknowledgement whose marker does not match the expected request marker
// is rejected outright, regardless of whether a canonical ID already
// exists. This models the A01/A02-style forged/prefix marker case at the
// state layer: state.go never accepts a candidate on comment ID alone.
func TestReconcileAcknowledgementRejectsMismatchedMarker(t *testing.T) {
	reservation := baseReservation()
	marker := Marker(reservation.RepositoryID, reservation.OriginalCommentID, reservation.Command, reservation.ResolvedVersion)
	record := Record{Reservation: reservation}

	_, err := ReconcileAcknowledgement(record, AcknowledgementCandidate{CommentID: "999", Marker: "<!-- forged -->"}, marker)
	if ErrorCode(err) != "acknowledgement_marker_mismatch" || ClassOf(err) != ErrorClassRequestRejected {
		t.Fatalf("error = %q/%q, want request_rejected acknowledgement_marker_mismatch", ClassOf(err), ErrorCode(err))
	}
}

func TestSealOpenRoundTripAndTamperDetection(t *testing.T) {
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Seal(signer, []byte(`{"phase":"reserved"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := Open(Ed25519Verifier{}, envelope, signer.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"phase":"reserved"}` {
		t.Fatalf("data = %q", data)
	}

	tampered := envelope
	tampered.Data = []byte(`{"phase":"complete"}`)
	if _, err := Open(Ed25519Verifier{}, tampered, signer.PublicKey()); ErrorCode(err) != "invalid_state_signature" {
		t.Fatalf("error = %q, want invalid_state_signature", ErrorCode(err))
	}

	otherSigner, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Ed25519Verifier{}, envelope, otherSigner.PublicKey()); ErrorCode(err) != "invalid_state_signature" {
		t.Fatalf("error = %q, want invalid_state_signature for wrong key", ErrorCode(err))
	}
}

func TestSignerFromSeedIsDeterministic(t *testing.T) {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	first, err := NewSignerFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSignerFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.PublicKey()) != string(second.PublicKey()) {
		t.Fatal("same seed produced different public keys")
	}
	if _, err := NewSignerFromSeed([]byte("too-short")); ErrorCode(err) != "invalid_signing_seed" {
		t.Fatalf("error = %q, want invalid_signing_seed", ErrorCode(err))
	}
}
