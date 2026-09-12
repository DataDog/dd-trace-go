// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// ValidateStateV3Record is the future v3 semantic gate. raw must be the exact
// canonical bytes read from authentication.Current's complete tree and blob.
// Authentication is external to raw, avoiding circular commit serialization.
func ValidateStateV3Record(raw []byte, record StateV3Record, policy StateV3Policy, authentication StateV3Authentication) error {
	invalid := func() error { return newReleaseError(ErrorClassStateConflict, "invalid_state_v3_record") }
	canonical, err := canonicalJSON(record)
	if err != nil || !bytes.Equal(raw, canonical) || !bytes.Equal(raw, authentication.Current.RawRecord) {
		return invalid()
	}
	decoded, err := DecodeStateV3Record(raw)
	// Coordination authentication is transient and intentionally excluded from
	// the canonical record. Compare its canonical representation, not Go values
	// that may carry the caller's external evidence.
	decodedCanonical, decodeErr := canonicalJSON(decoded)
	if err != nil || decodeErr != nil || !bytes.Equal(decodedCanonical, raw) || validateStateV3RecordCore(record, policy) != nil || !validStateV3Authentication(authentication, policy, record) || authentication.Coordination == nil || !validStateV3CrossLaneView(record.Reservation, policy, authentication, *authentication.Coordination) {
		return invalid()
	}
	return nil
}

func validateStateV3RecordCore(record StateV3Record, policy StateV3Policy) error {
	invalid := func() error { return newReleaseError(ErrorClassStateConflict, "invalid_state_v3_record") }
	if validateStateV3Policy(policy) != nil || record.SchemaVersion != StateV3SchemaVersion || !validStateV3Reservation(record.Reservation, policy) || !validStateV3Binding(record.Binding, record.Reservation) {
		return invalid()
	}
	phaseIndex, known := stateV3PhaseOrder[record.Phase]
	if !known || (phaseIndex >= stateV3PhaseOrder[StateV3PhasePrepared]) != (record.Prepared != nil) {
		return invalid()
	}
	if phaseIndex == 0 && (record.Commit != nil || len(record.MutationArms) != 0 || len(record.BranchEvidence) != 0 || len(record.TagPlans) != 0 || len(record.TagIntents) != 0 || len(record.TagObjectEvidence) != 0 || len(record.TagEvidence) != 0 || record.TestEvidence != nil || record.PreparePRIntent != nil || record.PreparePREvidence != nil || record.OutcomeEvidence != nil) {
		return invalid()
	}
	if phaseIndex >= stateV3PhaseOrder[StateV3PhaseBranchesPublished] && record.Commit == nil {
		return invalid()
	}
	if record.Prepared != nil && (!validStateV3Prepared(*record.Prepared, record.Reservation, policy) || !validStateV3TagPlans(record.TagPlans, record.Prepared.Mutation, record.Reservation, policy)) {
		return invalid()
	}
	if record.Commit == nil {
		if len(record.TagIntents) != 0 {
			return invalid()
		}
	} else if !validStateV3Commit(*record.Commit, *record.Prepared, policy) || !validStateV3TagIntents(record.TagIntents, record.TagPlans, *record.Commit) {
		return invalid()
	}
	if record.Prepared != nil && !validStateV3BranchEvidence(record.BranchEvidence, record) {
		return invalid()
	}
	if phaseIndex < stateV3PhaseOrder[StateV3PhaseTestsPassed] && (len(record.TagObjectEvidence) != 0 || len(record.TagEvidence) != 0) || len(record.TagObjectEvidence) != 0 && (record.Commit == nil || !validStateV3TagObjectEvidence(record.TagObjectEvidence, record.TagIntents, *record.Commit)) || len(record.TagEvidence) != 0 && (record.Commit == nil || !validStateV3TagEvidence(record.TagEvidence, record.TagObjectEvidence, *record.Commit)) || phaseIndex >= stateV3PhaseOrder[StateV3PhaseTagsPublished] && (len(record.TagEvidence) != len(record.TagIntents) || len(record.TagObjectEvidence) != len(record.TagIntents)) {
		return invalid()
	}
	if record.TestEvidence != nil && !validStateV3TestEvidence(*record.TestEvidence, record, policy) || phaseIndex >= stateV3PhaseOrder[StateV3PhaseTestsPassed] && record.TestEvidence == nil {
		return invalid()
	}
	if record.PreparePRIntent != nil && !validStateV3PreparePRIntent(*record.PreparePRIntent, record) || record.PreparePRIntent != nil && record.Reservation.Command != "release:prepare" || record.PreparePREvidence != nil && !validStateV3PreparePREvidence(*record.PreparePREvidence, record) || record.PreparePREvidence != nil && record.Reservation.Command != "release:prepare" {
		return invalid()
	}
	if record.OutcomeEvidence != nil && !validStateV3OutcomeEvidence(*record.OutcomeEvidence, record, policy) || phaseIndex == stateV3PhaseOrder[StateV3PhaseComplete] && record.OutcomeEvidence == nil {
		return invalid()
	}
	if !validStateV3Events(record) {
		return invalid()
	}
	return nil
}

func validStateV3Reservation(reservation StateV3Reservation, policy StateV3Policy) bool {
	if reservation.ContractVersion != ContractVersion || reservation.RepositoryID != policy.RepositoryID || reservation.RepositoryFullName != policy.RepositoryFullName || !validID(reservation.IssueNumber) || !validID(reservation.OriginalCommentID) || !validID(reservation.AcknowledgementCommentID) || !validID(reservation.ValidatedActorID) || !validStateV3Token(reservation.ValidatedActorLogin) || reservation.PolicyRevision != policy.PolicyRevision || !validStateV3OID(reservation.SourceOID) || !validStateV3VersionResolution(reservation, policy) {
		return false
	}
	context := Context{
		RepositoryID: reservation.RepositoryID, RepositoryFullName: reservation.RepositoryFullName,
		IssueNumber: reservation.IssueNumber, OriginalCommentID: reservation.OriginalCommentID,
		AcknowledgementCommentID: reservation.AcknowledgementCommentID,
		BodySnapshot:             reservation.BodySnapshot, PolicyRevision: reservation.PolicyRevision,
	}
	contextRaw, err := json.Marshal(context)
	if err != nil {
		return false
	}
	request, err := DecodeDispatchInputs(map[string]string{
		"contract_version": reservation.ContractVersion, "command": reservation.Command,
		"version": reservation.RequestedVersion, "context": string(contextRaw),
	})
	if err != nil || reservation.RequestKey != request.RequestKey || reservation.RequestSHA256 != request.RequestSHA256 || reservation.Marker != request.Marker {
		return false
	}
	parsed, err := ParseCommandBody(reservation.BodySnapshot, RequestIDs{
		RepositoryID: reservation.RepositoryID, RepositoryFullName: reservation.RepositoryFullName,
		IssueNumber: reservation.IssueNumber, OriginalCommentID: reservation.OriginalCommentID,
	})
	if err != nil || parsed.Command != reservation.Command || parsed.NormalizedVersion != reservation.RequestedVersion || parsed.RequestKey != reservation.RequestKey {
		return false
	}
	line, err := parseReleaseLine(reservation.ReleaseLine)
	if err != nil {
		return false
	}
	version, err := ParseReleaseVersion(reservation.GenerationVersion)
	if err != nil || version.Major != line.Major {
		return false
	}
	target, ok := stateV3TargetRef(reservation.Command, reservation.ReleaseLine)
	if !ok {
		return false
	}
	switch reservation.Command {
	case "release:prepare":
		resolved, resolvedErr := ParseReleaseVersion(reservation.ResolvedVersion)
		return resolvedErr == nil && resolved.Major == line.Major && resolved.Minor == line.Minor && resolved.Patch == 0 && resolved.Prerelease == prereleaseNone && reservation.DevelopmentVersion == reservation.GenerationVersion && reservation.SourceRef == "refs/heads/main" && version.Minor == line.Minor+1 && version.Prerelease == prereleaseDev
	case "release:promote":
		return reservation.ResolvedVersion == reservation.GenerationVersion && reservation.DevelopmentVersion == "" && reservation.SourceRef == target && version.Minor == line.Minor && version.Prerelease == prereleaseRC
	case "release:release":
		return reservation.ResolvedVersion == reservation.GenerationVersion && reservation.DevelopmentVersion == "" && reservation.SourceRef == target && version.Minor == line.Minor && version.Prerelease == prereleaseNone
	default:
		return false
	}
}

func validStateV3VersionResolution(reservation StateV3Reservation, policy StateV3Policy) bool {
	evidence := reservation.VersionResolution
	if evidence.RequestKey != reservation.RequestKey || evidence.RequestSHA256 != reservation.RequestSHA256 || evidence.Command != reservation.Command || evidence.RequestedVersion != reservation.RequestedVersion || evidence.ReleaseLine != reservation.ReleaseLine || !validStateV3SourceVersion(evidence.Source, reservation.SourceRef, reservation.SourceOID, policy) {
		return false
	}
	branches, branchesOK := stateV3RefObservationMap(evidence.RemoteRefs.Branches, "refs/heads/")
	tags, tagsOK := stateV3RefObservationMap(evidence.RemoteRefs.Tags, "refs/tags/")
	if !evidence.RemoteRefs.Complete || !branchesOK || !tagsOK || branches[evidence.Source.Ref] != evidence.Source.OID || !sort.StringsAreSorted(evidence.RemoteRefs.IncompleteTagVersions) || !uniqueStateV3Strings(evidence.RemoteRefs.IncompleteTagVersions) {
		return false
	}
	line, lineErr := parseReleaseLine(reservation.ReleaseLine)
	if lineErr != nil {
		return false
	}
	// Only root tags on the resolved release line participate in this
	// operation's incomplete-release decision, matching trustedIncompleteTagVersions.
	rootVersions := make([]string, 0)
	for ref := range tags {
		name := strings.TrimPrefix(ref, "refs/tags/")
		version, err := ParseReleaseVersion(name)
		if !strings.Contains(name, "/") && err == nil && version.Major == line.Major && version.Minor == line.Minor {
			rootVersions = append(rootVersions, name)
		}
	}
	sort.Strings(rootVersions)
	// Historical tagger-plan execution is deliberately non-authorizing in this
	// inactive semantic foundation. A future strict backend must execute the
	// policy-pinned artifact on the authenticated historical source tree and
	// exact-match its output before this input may reach ResolveVersion.
	// A self-contained collector attestation or self-hashed plan must never
	// decide whether a historical release is complete.
	if len(rootVersions) != 0 || len(evidence.RemoteRefs.IncompleteDerivations) != 0 || len(evidence.RemoteRefs.IncompleteTagVersions) != 0 {
		return false
	}
	derivedIncomplete := make([]string, 0)
	for index, derivation := range evidence.RemoteRefs.IncompleteDerivations {
		if derivation.Version != rootVersions[index] || !validStateV3IncompleteTagDerivation(derivation, tags, policy) {
			return false
		}
		if len(derivation.PresentTagRefs) != len(derivation.ExpectedTagRefs) {
			derivedIncomplete = append(derivedIncomplete, derivation.Version)
		}
	}
	if !reflect.DeepEqual(derivedIncomplete, evidence.RemoteRefs.IncompleteTagVersions) {
		return false
	}
	previousOperation := ""
	for _, operation := range evidence.ExistingOperations {
		key := operation.ReleaseLine + "\x00" + operation.RequestKey
		_, knownPhase := stateV3PhaseOrder[StateV3Phase(operation.Phase)]
		if key <= previousOperation || !validStateV3Text(operation.RequestKey, 256) || !lowerHexDigest(operation.RequestSHA256) || !isReleaseCommand(operation.Command) || !validStateV3Text(operation.ReleaseLine, 64) || !validStateV3Text(operation.ResolvedVersion, 128) || operation.DevelopmentVersion != "" && !validStateV3Text(operation.DevelopmentVersion, 128) || !knownPhase {
			return false
		}
		previousOperation = key
	}
	resolution, err := ResolveVersion(VersionResolutionInput{RequestKey: evidence.RequestKey, RequestSHA256: evidence.RequestSHA256, Command: evidence.Command, RequestedVersion: evidence.RequestedVersion, ReleaseLine: evidence.ReleaseLine, SourceVersion: evidence.Source.Version, RemoteRefs: RemoteRefs{Complete: true, Branches: branches, Tags: tags, IncompleteTagVersions: evidence.RemoteRefs.IncompleteTagVersions}, ExistingOperations: evidence.ExistingOperations})
	return err == nil && resolution == evidence.Result && resolution.Command == reservation.Command && resolution.ReleaseLine == reservation.ReleaseLine && resolution.RequestedVersion == reservation.RequestedVersion && resolution.SourceVersion == evidence.Source.Version && resolution.ResolvedVersion == reservation.ResolvedVersion && resolution.DevelopmentVersion == reservation.DevelopmentVersion && resolution.ReleaseBranch == reservation.ReleaseBranch && resolution.DevelopmentBranch == reservation.DevelopmentBranch
}

func validStateV3SourceVersion(source StateV3SourceVersionEvidence, ref, oid string, policy StateV3Policy) bool {
	if source.Ref != ref || source.OID != oid || source.Path != versionFileRelPathForValidation || !validStateV3OID(source.OID) || !validStateV3OID(source.BlobOID) || source.Commit.OID != source.OID || source.Commit.TreeOID != source.Tree.OID || !validStateV3SourceCommit(source.Commit) || !validStateV3SourceTree(source.Tree, source.Path, source.BlobOID) || !lowerHexDigest(source.SHA256) {
		return false
	}
	digest := sha256.Sum256(source.Raw)
	if source.SHA256 != hex.EncodeToString(digest[:]) || source.BlobOID != stateV3GitBlobOID(source.Raw) {
		return false
	}
	matches := regexp.MustCompile(`(?m)^var Tag = "([^"]+)"$`).FindAllSubmatch(source.Raw, -1)
	return len(matches) == 1 && string(matches[0][1]) == source.Version && validStateV3Text(source.Version, 128)
}

func validStateV3VerifiedCommit(commit StateV3StateCommitEvidence, policy StateV3Policy) bool {
	return validStateV3OID(commit.OID) && validStateV3OID(commit.TreeOID) && commit.RESTVerified && commit.RESTReason == StateV3RequiredRESTVerificationReason && commit.GraphQLSignatureValid && commit.WasSignedByGitHub && commit.SignatureState == StateV3RequiredSignatureState && commit.Roles == policy.CommitRoles
}

// validStateV3SourceCommit authenticates immutable source objects separately
// from state-writer provenance. Existing repository history is not required to
// have been authored by the future state App.
func validStateV3SourceCommit(commit StateV3StateCommitEvidence) bool {
	if !validStateV3OID(commit.OID) || !validStateV3OID(commit.TreeOID) {
		return false
	}
	// Source and historical commits predate the state-writing App. Their
	// authority comes from exact independently reread ref/tag-peel, commit,
	// tree, and blob bindings, not from GitHub-created state-commit roles.
	// If API identity observations are available, require their complete
	// non-ambiguous shape; otherwise an empty observation is legitimate.
	return commit.Roles == (StateV3CommitRoles{}) || validStateV3Roles(commit.Roles)
}

func validStateV3CompleteTree(tree StateV3StateTreeEvidence) bool {
	if !validStateV3OID(tree.OID) || !tree.Complete || tree.Truncated || len(tree.Entries) > 10_000 {
		return false
	}
	previous := ""
	for _, entry := range tree.Entries {
		if entry.Path <= previous || strings.HasPrefix(entry.Path, "/") || strings.Contains(entry.Path, "..") || entry.Mode != "100644" || entry.Type != "blob" || !validStateV3OID(entry.OID) {
			return false
		}
		previous = entry.Path
	}
	return true
}

func validStateV3SourceTree(tree StateV3StateTreeEvidence, path, blobOID string) bool {
	if !validStateV3CompleteTree(tree) {
		return false
	}
	for _, entry := range tree.Entries {
		if entry.Path == path {
			return entry.OID == blobOID
		}
	}
	return false
}

func validStateV3IncompleteTagDerivation(derivation StateV3IncompleteTagDerivation, tags map[string]string, policy StateV3Policy) bool {
	version, err := ParseReleaseVersion(derivation.Version)
	if err != nil || !validStateV3OID(derivation.ManifestOID) || tags["refs/tags/"+derivation.Version] != derivation.ManifestOID || !validStateV3OID(derivation.PeeledCommitOID) || !validStateV3OID(derivation.SourceTreeOID) || derivation.ToolPath != policy.TaggerArtifact.Path || derivation.ToolSHA256 != policy.TaggerArtifact.SHA256 || !reflect.DeepEqual(derivation.ToolArtifact, policy.TaggerArtifact) || !reflect.DeepEqual(derivation.Execution, StateV3PlanExecutionAttestation{SchemaVersion: "1", Collector: "strict_github_backend", ToolArtifact: policy.TaggerArtifact, SourceCommitOID: derivation.PeeledCommitOID, SourceTreeOID: derivation.SourceTreeOID, PlanSHA256: derivation.PlanSHA256, PlanBlobOID: derivation.PlanBlobOID, Verified: true}) || !lowerHexDigest(derivation.PlanSHA256) || !validStateV3OID(derivation.PlanBlobOID) || len(derivation.PlanRaw) == 0 || derivation.PlanBlobOID != stateV3GitBlobOID(derivation.PlanRaw) || !sort.StringsAreSorted(derivation.PresentTagRefs) || !uniqueStateV3Strings(derivation.PresentTagRefs) {
		return false
	}
	// The root tag, historical commit, tree, and approved tagger artifact are
	// distinct from the current operation's source evidence.
	if derivation.RootTag != (StateV3HistoricalTagEvidence{TagObjectOID: derivation.ManifestOID, ObjectType: "tag", TargetType: "commit", PeeledCommitOID: derivation.PeeledCommitOID, Signature: "absent"}) || derivation.HistoricalCommit.OID != derivation.PeeledCommitOID || derivation.HistoricalCommit.TreeOID != derivation.SourceTreeOID || !validStateV3SourceCommit(derivation.HistoricalCommit) || derivation.HistoricalTree.OID != derivation.SourceTreeOID || !validStateV3CompleteTree(derivation.HistoricalTree) {
		return false
	}
	digest := sha256.Sum256(derivation.PlanRaw)
	if hex.EncodeToString(digest[:]) != derivation.PlanSHA256 || validateJSONNoDuplicateKeys(derivation.PlanRaw, MaxStateV3DocumentBytes) != nil {
		return false
	}
	var raw map[string]any
	if json.Unmarshal(derivation.PlanRaw, &raw) != nil {
		return false
	}
	manifest, err := PlanManifestFromTaggerOutput(raw)
	branch := releaseBranchName(version.Major, version.Minor)
	if err != nil || manifest.SourceSHA != derivation.PeeledCommitOID || manifest.RequestedVersion != derivation.Version || raw["branch"] != branch {
		return false
	}
	expected := make([]string, len(manifest.ExpectedTags))
	for index, tag := range manifest.ExpectedTags {
		expected[index] = "refs/tags/" + tag
	}
	sort.Strings(expected)
	if !reflect.DeepEqual(expected, derivation.ExpectedTagRefs) || len(expected) == 0 {
		return false
	}
	rootExpected := false
	for _, ref := range expected {
		rootExpected = rootExpected || ref == "refs/tags/"+derivation.Version
	}
	if !rootExpected {
		return false
	}
	present := make([]string, 0, len(expected))
	for _, ref := range expected {
		if !validFullTagRef(ref) {
			return false
		}
		if _, ok := tags[ref]; ok {
			present = append(present, ref)
		}
	}
	return reflect.DeepEqual(present, derivation.PresentTagRefs)
}

func stateV3CoordinationClaimPath(line string) (string, bool) {
	parsed, err := parseReleaseLine(line)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("release-lines/%d.%d.json", parsed.Major, parsed.Minor), true
}

func stateV3ReservationClaimDigest(reservation StateV3Reservation) (string, bool) {
	copy := reservation
	copy.CoordinationClaim = StateV3ReleaseLineClaimEvidence{}
	// The coordination observation contains the arm commit that commits to
	// this claim. Excluding it from this immutable release digest prevents a
	// claim -> arm -> coordination-tree -> claim hash cycle; the external
	// authentication path validates that observation separately.
	copy.VersionResolution.Coordination = StateV3CoordinationObservation{}
	copy.VersionResolution.LaneHeads = nil
	return stateV3CanonicalDigest(copy)
}

func stateV3ResolutionDigest(evidence StateV3VersionResolutionEvidence) (string, bool) {
	copy := evidence
	// See stateV3ReservationClaimDigest: the CAS parent/claim relationship is
	// authenticated by the coordination arm and external history, not by a
	// self-referential digest inside the claim it creates.
	copy.Coordination = StateV3CoordinationObservation{}
	copy.LaneHeads = nil
	return stateV3CanonicalDigest(copy)
}

func validStateV3CoordinationPath(path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] != "release-lines" || !strings.HasSuffix(parts[1], ".json") {
		return false
	}
	line := "v" + strings.TrimSuffix(parts[1], ".json")
	_, err := parseReleaseLine(line)
	return err == nil
}

func stateV3CoordinationArm(snapshot StateV3CoordinationSnapshot) (*StateV3CoordinationMutationArm, bool) {
	if snapshot.Arm == nil {
		for _, entry := range snapshot.Tree.Entries {
			if entry.Path == stateV3CoordinationArmPath {
				return nil, false
			}
		}
		return nil, true
	}
	evidence := snapshot.Arm
	if evidence.Path != stateV3CoordinationArmPath || !lowerHexDigest(evidence.SHA256) || !validStateV3OID(evidence.BlobOID) || evidence.Arm.Ref != StateV3CoordinationRef || (evidence.Arm.Operation != "claim_acquire" && evidence.Arm.Operation != "claim_release") || !validStateV3CoordinationPath(evidence.Arm.ClaimPath) || !validStateV3Text(evidence.Arm.RequestKey, 256) || !validStateV3Text(evidence.Arm.ReleaseLine, 64) || !validStateV3Text(evidence.Arm.LaneRef, 128) || !validStateV3Text(evidence.Arm.ResolvedVersion, 128) || !validStateV3OID(evidence.Arm.ExpectedHeadOID) || evidence.Arm.Attempt != 1 || (evidence.Arm.ExpectedClaimBlobOID != "" && !validStateV3OID(evidence.Arm.ExpectedClaimBlobOID)) {
		return nil, false
	}
	if evidence.Arm.Operation == "claim_acquire" && (!lowerHexDigest(evidence.Arm.IntendedClaimSHA256) || evidence.Arm.ExpectedClaimBlobOID != "") || evidence.Arm.Operation == "claim_release" && (evidence.Arm.IntendedClaimSHA256 != "" || !validStateV3OID(evidence.Arm.ExpectedClaimBlobOID)) {
		return nil, false
	}
	raw, err := canonicalJSON(evidence.Arm)
	if err != nil || !bytes.Equal(raw, evidence.Raw) || evidence.BlobOID != stateV3GitBlobOID(raw) {
		return nil, false
	}
	digest := sha256.Sum256(raw)
	if evidence.SHA256 != hex.EncodeToString(digest[:]) {
		return nil, false
	}
	found := false
	for _, entry := range snapshot.Tree.Entries {
		if entry.Path == stateV3CoordinationArmPath {
			found = entry.OID == evidence.BlobOID
		}
	}
	if !found {
		return nil, false
	}
	return &evidence.Arm, true
}

func stateV3CoordinationClaims(snapshot StateV3CoordinationSnapshot) (map[string]StateV3ReleaseLineClaimEvidence, bool) {
	if !validStateV3CompleteTree(snapshot.Tree) || !sort.SliceIsSorted(snapshot.Claims, func(i, j int) bool { return snapshot.Claims[i].Path < snapshot.Claims[j].Path }) {
		return nil, false
	}
	if _, ok := stateV3CoordinationArm(snapshot); !ok {
		return nil, false
	}
	entries := make(map[string]string, len(snapshot.Tree.Entries))
	for _, entry := range snapshot.Tree.Entries {
		if entry.Path == stateV3CoordinationArmPath {
			continue
		}
		if !validStateV3CoordinationPath(entry.Path) {
			return nil, false
		}
		entries[entry.Path] = entry.OID
	}
	claims := make(map[string]StateV3ReleaseLineClaimEvidence, len(snapshot.Claims))
	for _, claim := range snapshot.Claims {
		if !validStateV3CoordinationDocument(claim) || entries[claim.Path] != claim.BlobOID {
			return nil, false
		}
		if _, duplicate := claims[claim.Path]; duplicate {
			return nil, false
		}
		claims[claim.Path] = claim
	}
	if len(claims) != len(entries) {
		return nil, false
	}
	return claims, true
}

func validStateV3CoordinationDocument(evidence StateV3ReleaseLineClaimEvidence) bool {
	path, ok := stateV3CoordinationClaimPath(evidence.Claim.ReleaseLine)
	if !ok || evidence.Path != path || evidence.Claim.State != "active" || evidence.Claim.Attempt != 1 || evidence.Claim.Phase != StateV3PhaseReserved || !isReleaseCommand(evidence.Claim.Command) || !lowerHexDigest(evidence.SHA256) || !validStateV3OID(evidence.BlobOID) || !validStateV3OID(evidence.Claim.LaneExpectedHeadOID) || !lowerHexDigest(evidence.Claim.ReservationSHA256) || !lowerHexDigest(evidence.Claim.VersionResolutionSHA256) {
		return false
	}
	raw, err := canonicalJSON(evidence.Claim)
	if err != nil || !bytes.Equal(raw, evidence.Raw) || evidence.BlobOID != stateV3GitBlobOID(raw) {
		return false
	}
	digest := sha256.Sum256(raw)
	return evidence.SHA256 == hex.EncodeToString(digest[:])
}

// validStateV3CoordinationAuthentication validates the shared claim index.
// Its checkpoint rotation is permitted only after all claims have been
// released, so every retained history segment is a sequence of one-file claim
// creations or ordinary terminal claim deletions.
func validStateV3CoordinationAuthentication(authentication StateV3CoordinationAuthentication, policy StateV3Policy) bool {
	if authentication.StateRef != policy.Coordination.StateRef || authentication.CheckpointOID != policy.Coordination.CheckpointOID || authentication.HeadOID != authentication.Current.Commit.OID || len(authentication.Predecessors)+1 > policy.Coordination.MaxHistoryCommits {
		return false
	}
	snapshots := append([]StateV3CoordinationSnapshot{authentication.Current}, authentication.Predecessors...)
	seen := map[string]bool{}
	for index := range snapshots {
		checkpoint := index == len(snapshots)-1
		snapshot := snapshots[index]
		if !validStateV3SnapshotShape(StateV3StateSnapshot{Commit: snapshot.Commit, Tree: snapshot.Tree}, policy, checkpoint) || seen[snapshot.Commit.OID] || (checkpoint && snapshot.Commit.OID != policy.Coordination.CheckpointOID) || (!checkpoint && snapshot.Commit.ParentOID != snapshots[index+1].Commit.OID) {
			return false
		}
		seen[snapshot.Commit.OID] = true
		if _, ok := stateV3CoordinationClaims(snapshot); !ok || (checkpoint && (len(snapshot.Claims) != 0 || snapshot.Release != nil || snapshot.Arm != nil)) {
			return false
		}
		if index+1 == len(snapshots) {
			continue
		}
		parent, child := snapshots[index+1], snapshot
		parentClaims, parentOK := stateV3CoordinationClaims(parent)
		childClaims, childOK := stateV3CoordinationClaims(child)
		parentArm, parentArmOK := stateV3CoordinationArm(parent)
		childArm, childArmOK := stateV3CoordinationArm(child)
		changes := stateV3TreeChanges(parent.Tree, child.Tree)
		if !parentOK || !childOK || !parentArmOK || !childArmOK || !reflect.DeepEqual(changes, child.Commit.ChangedPaths) {
			return false
		}
		if parentArm == nil && childArm != nil {
			if child.Release != nil || !reflect.DeepEqual(parentClaims, childClaims) || len(changes) != 1 || changes[0] != (StateV3ChangedPath{Path: stateV3CoordinationArmPath, ChildOID: child.Arm.BlobOID}) || childArm.ExpectedHeadOID != parent.Commit.OID {
				return false
			}
			continue
		}
		if parentArm != nil && childArm == nil {
			if len(changes) != 2 || child.Release != nil && parentArm.Operation != "claim_release" {
				return false
			}
			claimChange := StateV3ChangedPath{}
			for _, change := range changes {
				if change.Path != stateV3CoordinationArmPath {
					claimChange = change
				}
			}
			old, hadOld := parentClaims[claimChange.Path]
			new, hasNew := childClaims[claimChange.Path]
			switch {
			case parentArm.Operation == "claim_acquire" && !hadOld && hasNew:
				if child.Release != nil || claimChange.ParentOID != "" || claimChange.ChildOID != new.BlobOID || parentArm.ClaimPath != new.Path || parentArm.RequestKey != new.Claim.RequestKey || parentArm.ReleaseLine != new.Claim.ReleaseLine || parentArm.LaneRef != new.Claim.LaneRef || parentArm.ResolvedVersion != new.Claim.ResolvedVersion || parentArm.ExpectedClaimBlobOID != "" || parentArm.IntendedClaimSHA256 != new.SHA256 || !validStateV3ClaimAcquisition(new, child, parent, policy) {
					return false
				}
			case parentArm.Operation == "claim_release" && hadOld && !hasNew:
				if child.Release == nil || claimChange.ParentOID != old.BlobOID || claimChange.ChildOID != "" || parentArm.ClaimPath != old.Path || parentArm.RequestKey != old.Claim.RequestKey || parentArm.ReleaseLine != old.Claim.ReleaseLine || parentArm.LaneRef != old.Claim.LaneRef || parentArm.ResolvedVersion != old.Claim.ResolvedVersion || parentArm.ExpectedClaimBlobOID != old.BlobOID || parentArm.IntendedClaimSHA256 != "" || !validStateV3ClaimRelease(*child.Release, old, child, parent, policy, authentication.LaneTerminations) {
					return false
				}
			default:
				return false
			}
			continue
		}
		return false
	}
	claims, claimsOK := stateV3CoordinationClaims(authentication.Current)
	// A pending acquire still needs its result plus an ordinary release; a
	// pending release needs its one result. Without an arm each active claim
	// reserves its eventual ordinary release.
	arm, armOK := stateV3CoordinationArm(authentication.Current)
	if !claimsOK || !armOK {
		return false
	}
	remaining := len(claims)
	if arm != nil && arm.Operation == "claim_acquire" {
		remaining += 2
	}
	return stateV3CapacityFits(len(snapshots), remaining, policy.Coordination.MaxHistoryCommits)
}

func validStateV3CoordinationAttemptResponse(response StateV3MutationResponse, expectedOID string) bool {
	return response.Observation != "not_attempted" && validStateV3MutationResponse(response, expectedOID)
}

func validStateV3ClaimAcquisition(claim StateV3ReleaseLineClaimEvidence, child, parent StateV3CoordinationSnapshot, policy StateV3Policy) bool {
	return validStateV3CoordinationDocument(claim) && validStateV3VerifiedCommit(child.Commit, policy) && claim.Commit.OID == child.Commit.OID && claim.Commit.ParentOID == parent.Commit.OID && reflect.DeepEqual(claim.Tree, child.Tree) && claim.ObservedRefOID == child.Commit.OID && validStateV3CoordinationAttemptResponse(claim.Acquired, child.Commit.OID)
}

func validStateV3ClaimRelease(release StateV3CoordinationReleaseEvidence, claim StateV3ReleaseLineClaimEvidence, child, parent StateV3CoordinationSnapshot, policy StateV3Policy, terminations []StateV3Authentication) bool {
	if release.Path != claim.Path || release.ClaimSHA256 != claim.SHA256 || release.ClaimBlobOID != claim.BlobOID || release.ClaimCommitOID != claim.Commit.OID || release.ObservedRefOID != child.Commit.OID || !validStateV3CoordinationAttemptResponse(release.Response, child.Commit.OID) || !validStateV3VerifiedCommit(child.Commit, policy) {
		return false
	}
	return validStateV3LaneTermination(release.LaneTermination, claim, policy, terminations)
}

// validStateV3LaneTermination uses transient full lane history rather than
// trusting a persisted pair of isolated snapshots. The compact termination
// identifiers are durable; the bounded history is independently reread by the
// backend for claim release validation.
func validStateV3LaneTermination(termination StateV3LaneTerminationEvidence, claim StateV3ReleaseLineClaimEvidence, policy StateV3Policy, terminations []StateV3Authentication) bool {
	if termination.StateRef != claim.Claim.LaneRef || !validStateV3OID(termination.CheckpointOID) || !validStateV3OID(termination.CompleteRecordOID) || !lowerHexDigest(termination.CompleteRecordSHA256) || !validStateV3OID(termination.CompleteRecordBlobOID) || !validStateV3OID(termination.LeaseReleaseHeadOID) || termination.ObservedRefOID != termination.LeaseReleaseHeadOID || termination.CoordinationClaimOID != claim.Commit.OID || termination.CoordinationClaimBlobOID != claim.BlobOID || termination.CoordinationClaimSHA256 != claim.SHA256 {
		return false
	}
	for _, authentication := range terminations {
		if authentication.StateRef != termination.StateRef || authentication.CheckpointOID != termination.CheckpointOID || authentication.HeadOID != termination.LeaseReleaseHeadOID {
			continue
		}
		record, recordOK := stateV3SnapshotActiveRecord(authentication.Current, policy)
		if !recordOK || record == nil || record.Phase != StateV3PhaseComplete || authentication.Current.LeasePresent || authentication.Current.Commit.OID != termination.LeaseReleaseHeadOID || authentication.Current.RecordSHA256 != termination.CompleteRecordSHA256 || authentication.Current.RecordBlobOID != termination.CompleteRecordBlobOID || len(authentication.Predecessors) == 0 {
			continue
		}
		before := authentication.Predecessors[0]
		beforeRecord, beforeOK := stateV3SnapshotActiveRecord(before, policy)
		beforeLease, leaseOK := stateV3SnapshotLease(before)
		if !beforeOK || !leaseOK || beforeRecord == nil || beforeLease == nil || before.Commit.OID != termination.CompleteRecordOID || !reflect.DeepEqual(beforeRecord, record) || beforeRecord.Phase != StateV3PhaseComplete || before.RecordSHA256 != termination.CompleteRecordSHA256 || before.RecordBlobOID != termination.CompleteRecordBlobOID || !validStateV3Lease(*beforeLease, beforeRecord.Reservation, policy) {
			continue
		}
		if beforeRecord.Reservation.RequestKey != claim.Claim.RequestKey || beforeRecord.Reservation.RequestSHA256 != claim.Claim.RequestSHA256 || beforeRecord.Reservation.ReleaseLine != claim.Claim.ReleaseLine || beforeLease.CoordinationClaimOID != claim.Commit.OID || beforeLease.CoordinationClaimBlobOID != claim.BlobOID || beforeLease.CoordinationClaimSHA256 != claim.SHA256 || !validStateV3Authentication(authentication, policy, *record) {
			continue
		}
		return true
	}
	return false
}

func validStateV3CoordinationClaim(evidence StateV3ReleaseLineClaimEvidence, reservation StateV3Reservation, policy StateV3Policy, observation StateV3CoordinationObservation, external StateV3CoordinationAuthentication) bool {
	lane, ok := stateV3LaneForReservationFromResolution(reservation.ResolvedVersion, policy)
	path, pathOK := stateV3CoordinationClaimPath(reservation.ReleaseLine)
	reservationDigest, reservationOK := stateV3ReservationClaimDigest(reservation)
	resolutionDigest, resolutionOK := stateV3ResolutionDigest(reservation.VersionResolution)
	if !ok || !pathOK || !reservationOK || !resolutionOK || !validStateV3CoordinationAuthentication(external, policy) || !reflect.DeepEqual(observation.Head, external.Current.Commit) || !reflect.DeepEqual(observation.Tree, external.Current.Tree) || !reflect.DeepEqual(observation.Claims, external.Current.Claims) || evidence.Path != path || !validStateV3CoordinationDocument(evidence) || evidence.Claim.LaneRef != lane.StateRef || evidence.Claim.LaneExpectedHeadOID == "" || evidence.Claim.ReservationSHA256 != reservationDigest || evidence.Claim.VersionResolutionSHA256 != resolutionDigest {
		return false
	}
	parentClaims, parentOK := stateV3CoordinationClaims(external.Current)
	if !parentOK {
		return false
	}
	arm, armOK := stateV3CoordinationArm(external.Current)
	if _, exists := parentClaims[path]; exists || !armOK || arm == nil || arm.Operation != "claim_acquire" || arm.ClaimPath != path || arm.IntendedClaimSHA256 != evidence.SHA256 || !stateV3CapacityFits(len(external.Predecessors)+1, len(parentClaims)+1, policy.Coordination.MaxHistoryCommits) {
		return false
	}
	child := StateV3CoordinationSnapshot{Commit: evidence.Commit, Tree: evidence.Tree, Claims: append(append([]StateV3ReleaseLineClaimEvidence(nil), observation.Claims...), evidence)}
	sort.Slice(child.Claims, func(i, j int) bool { return child.Claims[i].Path < child.Claims[j].Path })
	if !validStateV3ClaimAcquisition(evidence, child, external.Current, policy) {
		return false
	}
	changes := stateV3TreeChanges(observation.Tree, evidence.Tree)
	return len(changes) == 2 && changes[0] == (StateV3ChangedPath{Path: stateV3CoordinationArmPath, ParentOID: external.Current.Arm.BlobOID}) && changes[1] == (StateV3ChangedPath{Path: path, ChildOID: evidence.BlobOID}) && reflect.DeepEqual(changes, evidence.Commit.ChangedPaths)
}

func validStateV3CrossLaneView(reservation StateV3Reservation, policy StateV3Policy, current StateV3Authentication, external StateV3CoordinationAuthentication) bool {
	evidence := reservation.VersionResolution
	observation := evidence.Coordination
	if !validStateV3CoordinationAuthentication(external, policy) || !reflect.DeepEqual(observation.Head, external.Current.Commit) || !reflect.DeepEqual(observation.Tree, external.Current.Tree) || !reflect.DeepEqual(observation.Claims, external.Current.Claims) {
		return false
	}
	claims, ok := stateV3CoordinationClaims(external.Current)
	if !ok {
		return false
	}
	derived := make([]ExistingOperation, 0, len(claims))
	for _, claim := range claims {
		derived = append(derived, ExistingOperation{RequestKey: claim.Claim.RequestKey, RequestSHA256: claim.Claim.RequestSHA256, Command: claim.Claim.Command, ReleaseLine: claim.Claim.ReleaseLine, ResolvedVersion: claim.Claim.ResolvedVersion, DevelopmentVersion: claim.Claim.DevelopmentVersion, Phase: string(claim.Claim.Phase)})
	}
	sort.Slice(derived, func(i, j int) bool {
		return derived[i].ReleaseLine+"\x00"+derived[i].RequestKey < derived[j].ReleaseLine+"\x00"+derived[j].RequestKey
	})
	path, pathOK := stateV3CoordinationClaimPath(reservation.ReleaseLine)
	if !pathOK || !reflect.DeepEqual(derived, evidence.ExistingOperations) || claims[path].Path != "" || !validStateV3CoordinationClaim(reservation.CoordinationClaim, reservation, policy, observation, external) {
		return false
	}
	snapshots := append([]StateV3StateSnapshot{current.Current}, current.Predecessors...)
	for index := 0; index+1 < len(snapshots); index++ {
		lease, leaseOK := stateV3SnapshotLease(snapshots[index])
		parentLease, parentOK := stateV3SnapshotLease(snapshots[index+1])
		if leaseOK && parentOK && lease != nil && lease.RequestKey == reservation.RequestKey && parentLease == nil {
			return snapshots[index].Commit.ParentOID == reservation.CoordinationClaim.Claim.LaneExpectedHeadOID && lease.CoordinationRef == policy.Coordination.StateRef && lease.CoordinationClaimPath == reservation.CoordinationClaim.Path && lease.CoordinationClaimOID == reservation.CoordinationClaim.Commit.OID && lease.CoordinationClaimBlobOID == reservation.CoordinationClaim.BlobOID && lease.CoordinationClaimSHA256 == reservation.CoordinationClaim.SHA256 && lease.CoordinationParentOID == observation.Head.OID
		}
	}
	return false
}

// validStateV3ObservedClaim validates an active claim's canonical tree/blob
// binding. Its lane record lifecycle is independently validated by that lane;
// the claim is the atomic authority used for conflict discovery.
func validStateV3ObservedClaim(evidence StateV3ReleaseLineClaimEvidence, policy StateV3Policy, observation StateV3CoordinationObservation) bool {
	path, ok := stateV3CoordinationClaimPath(evidence.Claim.ReleaseLine)
	if !ok || evidence.Path != path || evidence.Claim.Attempt != 1 || !isReleaseCommand(evidence.Claim.Command) || evidence.Claim.Phase == StateV3PhaseComplete || !validStateV3OID(evidence.Claim.LaneExpectedHeadOID) || !lowerHexDigest(evidence.Claim.ReservationSHA256) || !lowerHexDigest(evidence.Claim.VersionResolutionSHA256) || !validStateV3VerifiedCommit(evidence.Commit, policy) || evidence.Tree.OID != evidence.Commit.TreeOID || !validStateV3CompleteTree(evidence.Tree) || evidence.ObservedRefOID != evidence.Commit.OID || !validStateV3MutationResponse(evidence.Acquired, evidence.Commit.OID) {
		return false
	}
	raw, err := canonicalJSON(evidence.Claim)
	if err != nil || !bytes.Equal(raw, evidence.Raw) || evidence.BlobOID != stateV3GitBlobOID(raw) {
		return false
	}
	digest := sha256.Sum256(raw)
	if evidence.SHA256 != hex.EncodeToString(digest[:]) {
		return false
	}
	for _, entry := range evidence.Tree.Entries {
		if entry.Path == path {
			return entry.OID == evidence.BlobOID
		}
	}
	return false
}

// reservationClaimReservation locates the record bound to the authenticated
// current lane. This prevents a claim from authorizing a different operation.

func stateV3LaneForReservationFromResolution(versionRaw string, policy StateV3Policy) (StateV3LanePolicy, bool) {
	version, err := ParseReleaseVersion(versionRaw)
	if err != nil {
		return StateV3LanePolicy{}, false
	}
	if version.Patch == 0 {
		return policy.StateLanes.Minor, true
	}
	return policy.StateLanes.Patch, true
}

func stateV3LaneIndex(ref string) int {
	if ref == StateV3MinorStateRef {
		return 0
	}
	return 1
}

func validStateV3ObservedLaneHead(snapshot StateV3StateSnapshot, policy StateV3Policy, lane StateV3LanePolicy) (StateV3Record, bool, bool) {
	// A complete verified head tree binds the current lane lease and record.
	// Its bounded ancestry is independently revalidated when that operation is
	// resumed; embedding it here would recursively duplicate prepared bundles.
	if !validStateV3SnapshotShape(snapshot, policy, snapshot.Commit.OID == lane.CheckpointOID) {
		return StateV3Record{}, false, false
	}
	record, recordOK := stateV3SnapshotActiveRecord(snapshot, policy)
	lease, leaseOK := stateV3SnapshotLease(snapshot)
	if !leaseOK || !recordOK || (lease == nil && record == nil) {
		return StateV3Record{}, false, lease == nil && record == nil
	}
	if lease == nil && record != nil && record.Phase == StateV3PhaseComplete {
		return *record, false, true
	}
	if lease == nil || record == nil || !validStateV3Lease(*lease, record.Reservation, policy) {
		return StateV3Record{}, false, false
	}
	return *record, true, true
}

func validStateV3SnapshotShape(snapshot StateV3StateSnapshot, policy StateV3Policy, checkpoint bool) bool {
	commit := snapshot.Commit
	return validStateV3VerifiedCommit(commit, policy) && (!checkpoint && validStateV3OID(commit.ParentOID) || checkpoint && commit.ParentOID == "") && snapshot.Tree.OID == commit.TreeOID && snapshot.Tree.Complete && !snapshot.Tree.Truncated && len(snapshot.Tree.Entries) <= 10_000
}

func stateV3RefObservationMap(observations []StateV3RefObservation, prefix string) (map[string]string, bool) {
	result := make(map[string]string, len(observations))
	previous := ""
	for _, observation := range observations {
		if observation.Ref <= previous || !strings.HasPrefix(observation.Ref, prefix) || prefix == "refs/heads/" && !validBranchName(strings.TrimPrefix(observation.Ref, prefix)) || prefix == "refs/tags/" && !validFullTagRef(observation.Ref) || !validStateV3OID(observation.OID) {
			return nil, false
		}
		previous = observation.Ref
		result[observation.Ref] = observation.OID
	}
	return result, true
}

func uniqueStateV3Strings(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return false
		}
	}
	return true
}

func validStateV3Binding(binding StateV3ExecutionBinding, reservation StateV3Reservation) bool {
	return binding.RequestKey == reservation.RequestKey && binding.RequestSHA256 == reservation.RequestSHA256 && binding.PolicyRevision == reservation.PolicyRevision && validStateV3OID(binding.WorkflowSHA) && binding.WorkflowRef == "refs/heads/main" && binding.WorkflowPath == StateV3WorkflowPath && lowerHexDigest(binding.WorkflowFileSHA256) && validID(binding.WorkflowRunID) && binding.WorkflowRunAttempt > 0
}
