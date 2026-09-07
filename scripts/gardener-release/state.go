// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// OperationPhase is §13.5's phase progression. It only ever advances
// forward through this fixed order; state.go does not implement the
// generation/signing/publication work that earns each transition.
type OperationPhase string

const (
	PhaseReserved          OperationPhase = "reserved"
	PhaseSigned            OperationPhase = "signed"
	PhaseBranchesPublished OperationPhase = "branches_published"
	PhaseTestsPassed       OperationPhase = "tests_passed"
	PhaseTagsPublished     OperationPhase = "tags_published"
	PhaseComplete          OperationPhase = "complete"
)

// phaseOrder fixes the only permitted forward progression. A phase not in
// this map, or a transition that does not strictly advance through it, is
// rejected: state.go never lets an event move a record backwards.
var phaseOrder = map[OperationPhase]int{
	PhaseReserved:          0,
	PhaseSigned:            1,
	PhaseBranchesPublished: 2,
	PhaseTestsPassed:       3,
	PhaseTagsPublished:     4,
	PhaseComplete:          5,
}

// SourceRef is one recorded source ref/SHA pair, part of the immutable
// reservation.
type SourceRef struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

// BranchIntent is one recorded branch publication intent. ExpectedOldSHA is
// empty for a create-only intent (§13.5's "expected_old_sha_or_absent").
// DesiredSHA is "pending" until generation resolves it, matching
// "desired_sha_or_pending"; B09 is responsible for actually publishing it.
type BranchIntent struct {
	Ref            string `json:"ref"`
	ExpectedOldSHA string `json:"expected_old_sha,omitempty"`
	DesiredSHA     string `json:"desired_sha_or_pending"`
}

// Reservation holds §13.5's required immutable reservation fields. Every
// field here is fixed for the life of the request key: once persisted, no
// later event may change it. Fields that evolve (phase, workflow/tool SHA,
// branch intent progress, and the signed-output fields added at the
// `signed` transition) live on Record, not here.
type Reservation struct {
	SchemaVersion            string         `json:"schema_version"`
	RequestKey               string         `json:"request_key"`
	RequestSHA256            string         `json:"request_sha256"`
	RepositoryID             string         `json:"repository_id"`
	RepositoryFullName       string         `json:"repository_full_name"`
	IssueNumber              string         `json:"issue_number"`
	OriginalCommentID        string         `json:"original_comment_id"`
	AcknowledgementCommentID string         `json:"acknowledgement_comment_id"`
	Command                  string         `json:"command"`
	RequestedVersion         string         `json:"requested_version"`
	BodySnapshot             string         `json:"body_snapshot"`
	ValidatedActorID         string         `json:"validated_actor_id"`
	ValidatedActorLogin      string         `json:"validated_actor_login"`
	PolicyRevision           string         `json:"policy_revision"`
	ResolvedVersion          string         `json:"resolved_version"`
	ReleaseLine              string         `json:"release_line"`
	SourceRefs               []SourceRef    `json:"source_refs"`
	BranchIntents            []BranchIntent `json:"branch_intents"`
	CreatedAt                string         `json:"created_at"`
}

// SignedOutput holds §13.5's required signed-output fields, added at the
// `signed` phase transition. state.go stores this shape but does not
// produce it: B08 constructs the real values from a validated generation.
type SignedOutput struct {
	UnsignedSHA       string   `json:"unsigned_sha"`
	SourceSHA         string   `json:"source_sha"`
	TreeSHA           string   `json:"tree_sha"`
	ReleaseSHA        string   `json:"release_sha"`
	ToolDigest        string   `json:"tool_digest"`
	ValidatorDigest   string   `json:"validator_digest"`
	CommitParentSHA   string   `json:"commit_parent_sha"`
	SignerFingerprint string   `json:"signer_fingerprint"`
	ChangedPaths      []string `json:"changed_paths"`
	Tags              []TagRef `json:"tags"`
	Bundle            Bundle   `json:"bundle"`
}

// TagRef is one recorded release tag, required to be annotated and to peel
// directly to the recorded release commit (§13.5).
type TagRef struct {
	Name            string `json:"name"`
	Ref             string `json:"ref"`
	TagObjectSHA    string `json:"tag_object_sha"`
	PeeledCommitSHA string `json:"peeled_commit_sha"`
}

// Bundle records the recovery bundle's location and integrity evidence.
// state.go never writes the bundle contents; B08 owns bundle creation.
type Bundle struct {
	Path             string   `json:"path"`
	SHA256           string   `json:"sha256"`
	SizeBytes        int64    `json:"size_bytes"`
	PrerequisiteSHAs []string `json:"prerequisite_shas"`
}

// EventKind names a category of append-only evidence. state.go only
// defines the reservation-layer kinds; B07-B11 add outcome-specific kinds
// as they land, using the same append-only chain.
type EventKind string

const (
	EventReserved             EventKind = "reserved"
	EventPhaseAdvanced        EventKind = "phase_advanced"
	EventAcknowledgementBound EventKind = "acknowledgement_bound"
	EventFailureObserved      EventKind = "failure_observed"
)

// Event is one immutable, append-only record on an operation. Sequence
// starts at 1. PreviousDigest is the zero value ("") only for sequence 1;
// every later event's PreviousDigest must equal the prior event's Digest,
// so a broken or reordered chain is detectable without trusting the store.
type Event struct {
	RequestKey     string          `json:"request_key"`
	Sequence       int             `json:"sequence"`
	PreviousDigest string          `json:"previous_event_digest"`
	Kind           EventKind       `json:"kind"`
	Evidence       json.RawMessage `json:"evidence"`
	Digest         string          `json:"digest"`
}

// Record is the complete, loaded state for one request key: the immutable
// reservation, the current phase, and every event applied so far in order.
// SignedOutput is populated once phase reaches PhaseSigned or later.
type Record struct {
	Reservation  Reservation
	Phase        OperationPhase
	WorkflowSHA  string
	ToolSHA      string
	SignedOutput *SignedOutput
	Events       []Event
}

// computeEventDigest deterministically hashes one event's chain-relevant
// fields (everything except its own digest) so a store cannot silently
// substitute a different event with the same sequence number.
func computeEventDigest(event Event) (string, error) {
	unsigned := event
	unsigned.Digest = ""
	encoded, err := canonicalJSON(unsigned)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// canonicalJSON encodes v as compact JSON with map keys sorted, so the same
// logical value always hashes to the same bytes regardless of map
// iteration order. Event and Reservation only use structs/slices, whose
// encoding/json field order is already stable; this helper exists so a
// future evidence payload with an object body under Evidence can still be
// hashed deterministically when re-serialized during verification.
func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	return marshalCanonical(generic)
}

func marshalCanonical(v any) ([]byte, error) {
	switch value := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := []byte{'{'}
		for i, key := range keys {
			if i > 0 {
				out = append(out, ',')
			}
			encodedKey, err := json.Marshal(key)
			if err != nil {
				return nil, err
			}
			out = append(out, encodedKey...)
			out = append(out, ':')
			encodedValue, err := marshalCanonical(value[key])
			if err != nil {
				return nil, err
			}
			out = append(out, encodedValue...)
		}
		out = append(out, '}')
		return out, nil
	case []any:
		out := []byte{'['}
		for i, item := range value {
			if i > 0 {
				out = append(out, ',')
			}
			encoded, err := marshalCanonical(item)
			if err != nil {
				return nil, err
			}
			out = append(out, encoded...)
		}
		out = append(out, ']')
		return out, nil
	default:
		return json.Marshal(value)
	}
}

// AppendEvent extends events with a new event of the given kind and
// evidence, computing its digest chain from the last event (or the zero
// value if events is empty). It does not mutate its input slice.
func AppendEvent(events []Event, requestKey string, kind EventKind, evidence json.RawMessage) ([]Event, error) {
	previousDigest := ""
	sequence := 1
	if len(events) > 0 {
		last := events[len(events)-1]
		previousDigest = last.Digest
		sequence = last.Sequence + 1
	}
	event := Event{
		RequestKey:     requestKey,
		Sequence:       sequence,
		PreviousDigest: previousDigest,
		Kind:           kind,
		Evidence:       evidence,
	}
	digest, err := computeEventDigest(event)
	if err != nil {
		return nil, wrapReleaseError(ErrorClassStateConflict, "event_digest_failed", err)
	}
	event.Digest = digest
	return append(append([]Event(nil), events...), event), nil
}

// VerifyEventChain re-derives every event's digest and checks the
// sequence/previous-digest linkage. It fails closed on the first broken
// link: a store must never let an operator or resume path treat a partial
// or reordered chain as verified evidence.
func VerifyEventChain(requestKey string, events []Event) error {
	previousDigest := ""
	for i, event := range events {
		if event.RequestKey != requestKey {
			return newReleaseError(ErrorClassStateConflict, "event_request_key_mismatch")
		}
		if event.Sequence != i+1 {
			return newReleaseError(ErrorClassStateConflict, "event_sequence_gap")
		}
		if event.PreviousDigest != previousDigest {
			return newReleaseError(ErrorClassStateConflict, "event_chain_broken")
		}
		want := event.Digest
		recomputed, err := computeEventDigest(event)
		if err != nil {
			return wrapReleaseError(ErrorClassStateConflict, "event_digest_failed", err)
		}
		if recomputed != want {
			return newReleaseError(ErrorClassStateConflict, "event_digest_mismatch")
		}
		previousDigest = want
	}
	return nil
}

// immutableReservationDiff reports the first field that differs between two
// reservations for the same request key, or "" if they match. Used to
// reject a differing body/request hash (S03) and any other attempted
// mutation of an immutable field on an existing reservation.
func immutableReservationDiff(existing, incoming Reservation) string {
	switch {
	case existing.SchemaVersion != incoming.SchemaVersion:
		return "schema_version"
	case existing.RequestKey != incoming.RequestKey:
		return "request_key"
	case existing.RequestSHA256 != incoming.RequestSHA256:
		return "request_sha256"
	case existing.RepositoryID != incoming.RepositoryID:
		return "repository_id"
	case existing.RepositoryFullName != incoming.RepositoryFullName:
		return "repository_full_name"
	case existing.IssueNumber != incoming.IssueNumber:
		return "issue_number"
	case existing.OriginalCommentID != incoming.OriginalCommentID:
		return "original_comment_id"
	case existing.Command != incoming.Command:
		return "command"
	case existing.RequestedVersion != incoming.RequestedVersion:
		return "requested_version"
	case existing.BodySnapshot != incoming.BodySnapshot:
		return "body_snapshot"
	case existing.ValidatedActorID != incoming.ValidatedActorID:
		return "validated_actor_id"
	case existing.ValidatedActorLogin != incoming.ValidatedActorLogin:
		return "validated_actor_login"
	case existing.PolicyRevision != incoming.PolicyRevision:
		return "policy_revision"
	case existing.ResolvedVersion != incoming.ResolvedVersion:
		return "resolved_version"
	case existing.ReleaseLine != incoming.ReleaseLine:
		return "release_line"
	case !equalSourceRefs(existing.SourceRefs, incoming.SourceRefs):
		return "source_refs"
	}
	// AcknowledgementCommentID is deliberately excluded: §5.6 permits a
	// verified, authentic duplicate acknowledgement to reconcile without
	// changing the stored canonical ID. ReconcileAcknowledgement enforces
	// that only an authentic candidate can pass, and never rewrites the
	// stored value itself.
	return ""
}

// equalSourceRefs reports whether two source_refs[] slices are the same
// ref/SHA pairs in the same order. source_refs is part of §13.5's
// immutable reservation: once persisted, a retry or resume must not
// silently swap in a different source commit for the same request key.
func equalSourceRefs(a, b []SourceRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ReservationDecision is the outcome of attempting to reserve a request.
// Reserved is true only when a new reservation was accepted, or an
// identical retry matched an existing one (S04's idempotent reservation).
// Reserved is false when an existing, non-identical operation blocks the
// request (S02 loser, S03 conflict, or a same-line incomplete operation).
type ReservationDecision struct {
	Reserved bool
	Record   Record
}

// ReserveOperation decides whether incoming may become (or already is) the
// reservation for its request key, given the currently loaded state for
// every operation that shares its release line. It performs no I/O: the
// caller loads existing records and persists the decision.
//
// incompleteSameLine must contain every other operation (different request
// key) whose ReleaseLine matches incoming.ReleaseLine and whose Phase is
// not PhaseComplete. Blocking on that set implements V04/§13.5's "no
// guessed repair": a second operation on the same line cannot start while
// one is in flight, even if its own evidence looks fine in isolation.
func ReserveOperation(existing *Record, incoming Reservation, incompleteSameLine []Record) (ReservationDecision, error) {
	if existing != nil {
		if diff := immutableReservationDiff(existing.Reservation, incoming); diff != "" {
			return ReservationDecision{}, newReleaseError(ErrorClassStateConflict, "immutable_field_changed")
		}
		// Same request key, same immutable fields: this is a retry of an
		// already-reserved (or further-advanced) operation, not a new
		// reservation. S04: the record write already happened; report the
		// existing record as success rather than reserving again.
		return ReservationDecision{Reserved: true, Record: *existing}, nil
	}
	for _, other := range incompleteSameLine {
		if other.Reservation.RequestKey == incoming.RequestKey {
			continue
		}
		return ReservationDecision{}, newReleaseError(ErrorClassStateConflict, "incomplete_operation_on_release_line")
	}
	record := Record{Reservation: incoming, Phase: PhaseReserved}
	evidence, err := json.Marshal(struct {
		ResolvedVersion string `json:"resolved_version"`
		ReleaseLine     string `json:"release_line"`
	}{ResolvedVersion: incoming.ResolvedVersion, ReleaseLine: incoming.ReleaseLine})
	if err != nil {
		return ReservationDecision{}, wrapReleaseError(ErrorClassStateConflict, "event_digest_failed", err)
	}
	events, err := AppendEvent(nil, incoming.RequestKey, EventReserved, evidence)
	if err != nil {
		return ReservationDecision{}, err
	}
	record.Events = events
	return ReservationDecision{Reserved: true, Record: record}, nil
}

// AcknowledgementCandidate is an already-verified acknowledgement, fetched
// and authenticated by the caller (exact Gardener identity, exact release
// marker, correct repository/issue/original-comment binding). state.go
// never fetches or authenticates comments itself; it only reconciles
// candidates the caller already trusts.
type AcknowledgementCandidate struct {
	CommentID string
	Marker    string
}

// ReconcileAcknowledgement implements §5.6's remapping rule at the state
// layer. If record has no stored acknowledgement ID, candidate becomes
// canonical. If record already has one, a candidate with a different ID is
// accepted for reconciliation (e.g. this run's dispatch input) only if its
// marker matches the stored request's marker; the stored canonical ID is
// never overwritten. A candidate for a different request key is rejected.
func ReconcileAcknowledgement(record Record, candidate AcknowledgementCandidate, expectedMarker string) (string, error) {
	if candidate.Marker != expectedMarker {
		return "", newReleaseError(ErrorClassRequestRejected, "acknowledgement_marker_mismatch")
	}
	stored := record.Reservation.AcknowledgementCommentID
	if stored == "" {
		return candidate.CommentID, nil
	}
	// Authentic candidate for the same request: keep the stored canonical
	// ID. A later verified duplicate never displaces the first winner.
	return stored, nil
}

// KnownPhase reports whether phase is one of §13.5's fixed progression
// values. A store or resume path must reject any other value rather than
// guessing an ordering for it.
func KnownPhase(phase OperationPhase) bool {
	_, ok := phaseOrder[phase]
	return ok
}

// AdvancePhase validates that next strictly follows current in §13.5's
// fixed order and returns it. It never allows staying at the same phase or
// moving backwards, and it rejects an unknown phase on either side.
func AdvancePhase(current, next OperationPhase) (OperationPhase, error) {
	currentRank, ok := phaseOrder[current]
	if !ok {
		return "", newReleaseError(ErrorClassStateConflict, "unknown_operation_phase")
	}
	nextRank, ok := phaseOrder[next]
	if !ok {
		return "", newReleaseError(ErrorClassStateConflict, "unknown_operation_phase")
	}
	if nextRank <= currentRank {
		return "", newReleaseError(ErrorClassStateConflict, "phase_regression")
	}
	return next, nil
}

// LoadResult is what a StateStore returns for one request key.
type LoadResult struct {
	Record Record
	Found  bool
	// RemoteHead is the state-branch commit SHA the record was read from.
	// Callers must supply this back as the expected parent on their next
	// write so the store can detect a concurrent update (S02).
	RemoteHead string
}

// StatePaths builds the state-branch paths from validated decimal IDs
// only. Callers must validate repositoryID and originalCommentID with
// validID before calling; StatePaths itself re-validates and fails closed
// rather than accepting a path built from an unchecked dispatch input or
// API response value.
func StatePaths(repositoryID, originalCommentID string) (StateRequestPaths, error) {
	if !validID(repositoryID) || !validID(originalCommentID) {
		return StateRequestPaths{}, newReleaseError(ErrorClassContractMismatch, "unsafe_id")
	}
	base := "requests/" + repositoryID + "/" + originalCommentID
	return StateRequestPaths{
		Base:           base,
		Reservation:    base + "/reservation.json",
		Signed:         base + "/signed.json",
		RecoveryBundle: base + "/recovery.bundle",
		EventsDir:      base + "/events",
	}, nil
}

// StateRequestPaths are the fixed relative paths for one request key's
// state, all derived from validated decimal IDs (§13.5).
type StateRequestPaths struct {
	Base           string
	Reservation    string
	Signed         string
	RecoveryBundle string
	EventsDir      string
}

// EventPath returns the path for the given 1-based event sequence number,
// zero-padded to six digits as required by §13.5's example paths.
func (p StateRequestPaths) EventPath(sequence int) string {
	return fmt.Sprintf("%s/%06d.json", p.EventsDir, sequence)
}

// StateStore is the durable-record boundary. Implementations must fail
// closed: a missing branch, invalid signature, broken event chain, or
// unknown schema is an error, never a trigger to bootstrap fresh state.
type StateStore interface {
	// LoadState reads and verifies the current record for requestKey, or
	// reports Found=false if no reservation exists yet. It must return an
	// error rather than Found=false when the branch itself is missing or
	// unreadable in a way that cannot distinguish "no such request" from
	// "state is unavailable."
	LoadState(ctx context.Context, requestKey string) (LoadResult, error)

	// IncompleteOperationsOnLine returns every currently loaded record
	// whose ReleaseLine matches releaseLine and whose Phase is not
	// PhaseComplete, for use with ReserveOperation. It must not silently
	// skip records it cannot fully verify; a verification failure is an
	// error, not an empty result.
	IncompleteOperationsOnLine(ctx context.Context, releaseLine string) ([]Record, error)

	// PersistReservation writes decision.Record as the state for its
	// request key, using expectedParent as the state branch's expected
	// current commit SHA (from a prior LoadState/LoadResult.RemoteHead, or
	// "" for a state branch the caller has verified is otherwise empty of
	// this request key). If the actual remote head has moved, it must
	// return a state_conflict error and perform no write (S02's loser
	// reloads or stops; it never overwrites). On success it returns the
	// new head SHA.
	PersistReservation(ctx context.Context, decision ReservationDecision, expectedParent string) (string, error)
}
