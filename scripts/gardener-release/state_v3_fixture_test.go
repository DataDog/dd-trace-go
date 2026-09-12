// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
)

const (
	v3OIDa = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	v3OIDb = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	v3OIDc = "cccccccccccccccccccccccccccccccccccccccc"
	v3OIDd = "dddddddddddddddddddddddddddddddddddddddd"
	v3SHAa = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	v3SHAb = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	v3SHAc = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func fixtureStateV3Ruleset(id, revision, namespace string, includes, rules []string, appBypass bool) StateV3RulesetPolicy {
	actors := []StateV3RulesetBypassActor{}
	if appBypass {
		actors = []StateV3RulesetBypassActor{{ActorID: "10001", ActorType: "Integration", Mode: "always"}}
	}
	policy := StateV3RulesetPolicy{ID: id, Revision: revision, Namespace: namespace, IncludePatterns: includes, ExcludePatterns: []string{}, Enforcement: "active"}
	for _, rule := range rules {
		policy.Rules = append(policy.Rules, StateV3RulesetRule{Type: rule})
	}
	conditionsDigest, _ := stateV3CanonicalDigest(struct {
		Namespace string   `json:"namespace"`
		Includes  []string `json:"includes"`
		Excludes  []string `json:"excludes"`
	}{policy.Namespace, policy.IncludePatterns, policy.ExcludePatterns})
	rulesDigest, _ := stateV3CanonicalDigest(policy.Rules)
	policy.Attestation = StateV3RulesetAdminAttestation{
		SchemaVersion: "1", RulesetID: id, RulesetRevision: revision,
		BypassVisibility: "complete", AdministratorBypassDisabled: true,
		BypassActors:     actors,
		Reviewer:         StateV3AssociatedIdentity{Login: "synthetic-policy-reviewer", DatabaseID: "50006", Type: "User"},
		ObservedAt:       "2026-09-12T06:10:00Z",
		ConditionsSHA256: conditionsDigest, RulesSHA256: rulesDigest, SemanticNamespaceValidated: true,
	}
	return policy
}

func refreshStateV3RulesetAttestation(policy *StateV3RulesetPolicy) {
	conditionsDigest, _ := stateV3CanonicalDigest(struct {
		Namespace string   `json:"namespace"`
		Includes  []string `json:"includes"`
		Excludes  []string `json:"excludes"`
	}{policy.Namespace, policy.IncludePatterns, policy.ExcludePatterns})
	rulesDigest, _ := stateV3CanonicalDigest(policy.Rules)
	policy.Attestation.ConditionsSHA256 = conditionsDigest
	policy.Attestation.RulesSHA256 = rulesDigest
}

func fixtureStateV3Policy() StateV3Policy {
	artifactRaw := []byte("synthetic autoreleasetagger artifact")
	artifactDigest := sha256.Sum256(artifactRaw)
	artifact := StateV3TrustedArtifact{Path: "scripts/autoreleasetagger", Raw: artifactRaw, SHA256: hex.EncodeToString(artifactDigest[:]), BlobOID: stateV3GitBlobOID(artifactRaw), TreeOID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Tree: StateV3StateTreeEvidence{OID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Complete: true, Entries: []StateV3StateTreeEntry{{Path: "scripts/autoreleasetagger", Mode: "100644", Type: "blob", OID: stateV3GitBlobOID(artifactRaw)}}}}
	return StateV3Policy{
		SchemaVersion:      StateV3PolicySchemaVersion,
		PolicyRevision:     v3SHAb,
		RepositoryID:       "123",
		RepositoryFullName: RepositoryFullName,
		StateLanes: StateV3LanePolicies{
			Minor: StateV3LanePolicy{StateRef: StateV3MinorStateRef, CheckpointOID: v3OIDa, MaxHistoryCommits: 512},
			Patch: StateV3LanePolicy{StateRef: StateV3PatchStateRef, CheckpointOID: v3OIDb, MaxHistoryCommits: 512},
		},
		Coordination:   StateV3CoordinationPolicy{StateRef: StateV3CoordinationRef, CheckpointOID: v3OIDc, MaxHistoryCommits: 512},
		TaggerArtifact: artifact,
		App: StateV3AppIdentity{
			AppID: "10001", InstallationID: "20002", Slug: "synthetic-release-app",
			BotLogin: "synthetic-release-app[bot]", BotDatabaseID: "30003",
		},
		CommitRoles: StateV3CommitRoles{
			AuthorRaw:              StateV3RawIdentity{Name: "github-actions[bot]", Email: "41898282+github-actions[bot]@users.noreply.github.com"},
			AuthorREST:             StateV3AssociatedIdentity{Login: "synthetic-release-app[bot]", DatabaseID: "30003", Type: "Bot"},
			AuthorGraphQL:          StateV3AssociatedIdentity{Login: "synthetic-release-app[bot]", DatabaseID: "30003", Type: "User"},
			CommitterRaw:           StateV3RawIdentity{Name: "GitHub", Email: "noreply@github.com"},
			CommitterREST:          StateV3OptionalAssociatedIdentity{Present: true, Identity: StateV3AssociatedIdentity{Login: "synthetic-platform-committer", DatabaseID: "40004", Type: "User"}},
			CommitterGraphQL:       StateV3OptionalAssociatedIdentity{},
			SignatureSignerGraphQL: StateV3AssociatedIdentity{Login: "synthetic-platform-signer", DatabaseID: "50005", Type: "User"},
		},
		Tagger: StateV3RawIdentity{Name: "Synthetic Release App", Email: "synthetic-release-app@example.invalid"},
		Rulesets: StateV3RulesetPolicies{
			StateHistoryProtection:          fixtureStateV3Ruleset("60006", "2026-09-12T06:00:00Z", "state", []string{StateV3MinorStateRef, StateV3PatchStateRef}, []string{"deletion", "non_fast_forward", "required_signatures"}, false),
			StateUpdateAuthorization:        fixtureStateV3Ruleset("60007", "2026-09-12T06:00:01Z", "state", []string{StateV3MinorStateRef, StateV3PatchStateRef}, []string{"update"}, true),
			CoordinationHistoryProtection:   fixtureStateV3Ruleset("60012", "2026-09-12T06:00:06Z", "coordination", []string{StateV3CoordinationRef}, []string{"deletion", "non_fast_forward", "required_signatures"}, false),
			CoordinationUpdateAuthorization: fixtureStateV3Ruleset("60013", "2026-09-12T06:00:07Z", "coordination", []string{StateV3CoordinationRef}, []string{"update"}, true),
			BranchCreationAuthorization:     fixtureStateV3Ruleset("60008", "2026-09-12T06:00:02Z", "release_branches", []string{"refs/heads/synthetic-release-namespace/*"}, []string{"creation"}, true),
			BranchUpdateAuthorization:       fixtureStateV3Ruleset("60009", "2026-09-12T06:00:03Z", "release_branches", []string{"refs/heads/synthetic-release-namespace/*"}, []string{"update"}, true),
			TagCreationAuthorization:        fixtureStateV3Ruleset("60010", "2026-09-12T06:00:04Z", "release_tags", []string{"refs/tags/synthetic-release-namespace/*"}, []string{"creation"}, true),
			TagImmutability:                 fixtureStateV3Ruleset("60011", "2026-09-12T06:00:05Z", "release_tags", []string{"refs/tags/synthetic-release-namespace/*"}, []string{"deletion", "non_fast_forward", "required_signatures", "update"}, false),
		},
		Test:     StateV3TestPolicy{WorkflowID: "70001", WorkflowPath: MainBranchTestWorkflowPath, WorkflowSHA256: v3SHAa, Event: "push", RequiredJobs: []string{"required-a", "required-b"}, DeadlineSeconds: 1800},
		Image:    StateV3ImagePolicy{WorkflowID: "70002", WorkflowPath: ImageWorkflowPath, WorkflowSHA256: v3SHAb, ChildWorkflowSHA256: v3SHAc},
		Feedback: StateV3FeedbackPolicy{GardenerAuthorID: "70003", GardenerAuthorLogin: "synthetic-gardener"},
		Limits: StateV3CapacityLimits{
			PreparedBundleBytes:  MaxStateV3PreparedBundleBytes,
			DecodedAdditionBytes: MaxStateV3DecodedAdditionBytes,
			ReleaseFileChanges:   MaxStateV3ReleaseFileChanges,
		},
	}
}

func fixtureStateV3VersionResolution(t *testing.T, reservation *StateV3Reservation, sourceVersion string) {
	t.Helper()
	raw := []byte("package version\n\nvar Tag = \"" + sourceVersion + "\"\n")
	digest := sha256.Sum256(raw)
	source := StateV3SourceVersionEvidence{Ref: reservation.SourceRef, OID: reservation.SourceOID, Path: versionFileRelPathForValidation, Raw: raw, SHA256: hex.EncodeToString(digest[:]), BlobOID: stateV3GitBlobOID(raw), Version: sourceVersion, Commit: StateV3StateCommitEvidence{OID: reservation.SourceOID, TreeOID: v3OIDc, ChangedPaths: []StateV3ChangedPath{}, RESTVerified: true, RESTReason: StateV3RequiredRESTVerificationReason, GraphQLSignatureValid: true, WasSignedByGitHub: true, SignatureState: StateV3RequiredSignatureState, Roles: fixtureStateV3Policy().CommitRoles}, Tree: StateV3StateTreeEvidence{OID: v3OIDc, Complete: true, Entries: []StateV3StateTreeEntry{{Path: versionFileRelPathForValidation, Mode: "100644", Type: "blob", OID: stateV3GitBlobOID(raw)}}}}
	remote := StateV3RemoteRefsEvidence{Complete: true, Branches: []StateV3RefObservation{{Ref: reservation.SourceRef, OID: reservation.SourceOID}}, Tags: []StateV3RefObservation{}, IncompleteTagVersions: []string{}, IncompleteDerivations: []StateV3IncompleteTagDerivation{}}
	evidence := StateV3VersionResolutionEvidence{RequestKey: reservation.RequestKey, RequestSHA256: reservation.RequestSHA256, Command: reservation.Command, RequestedVersion: reservation.RequestedVersion, ReleaseLine: reservation.ReleaseLine, Source: source, RemoteRefs: remote, ExistingOperations: []ExistingOperation{}, Coordination: fixtureStateV3CoordinationObservation(t)}
	resolution, err := ResolveVersion(VersionResolutionInput{RequestKey: evidence.RequestKey, RequestSHA256: evidence.RequestSHA256, Command: evidence.Command, RequestedVersion: evidence.RequestedVersion, ReleaseLine: evidence.ReleaseLine, SourceVersion: sourceVersion, RemoteRefs: RemoteRefs{Complete: true, Branches: map[string]string{reservation.SourceRef: reservation.SourceOID}, Tags: map[string]string{}, IncompleteTagVersions: []string{}}, ExistingOperations: []ExistingOperation{}})
	if err != nil {
		t.Fatalf("fixture version resolution: %v", err)
	}
	evidence.Result = resolution
	reservation.ResolvedVersion = resolution.ResolvedVersion
	reservation.DevelopmentVersion = resolution.DevelopmentVersion
	reservation.ReleaseBranch = resolution.ReleaseBranch
	reservation.DevelopmentBranch = resolution.DevelopmentBranch
	reservation.VersionResolution = evidence
	fixtureStateV3CoordinationClaim(t, reservation)
}

func fixtureStateV3SetCoordinationClaims(t *testing.T, evidence *StateV3VersionResolutionEvidence, claims []StateV3ReleaseLineClaimEvidence) {
	t.Helper()
	policy := fixtureStateV3Policy()
	chronological := []StateV3CoordinationSnapshot{{Commit: StateV3StateCommitEvidence{OID: policy.Coordination.CheckpointOID, TreeOID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", ChangedPaths: []StateV3ChangedPath{}, RESTVerified: true, RESTReason: StateV3RequiredRESTVerificationReason, GraphQLSignatureValid: true, WasSignedByGitHub: true, SignatureState: StateV3RequiredSignatureState, Roles: policy.CommitRoles}, Tree: StateV3StateTreeEvidence{OID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Complete: true, Entries: []StateV3StateTreeEntry{}}, Claims: []StateV3ReleaseLineClaimEvidence{}}}
	for index := range claims {
		claim := &claims[index]
		parent := chronological[len(chronological)-1]
		arm := fixtureStateV3CoordinationArm(t, parent, claim.Path, "claim_acquire", "", claim.Claim, 100+index*3, policy)
		claim.Commit.ParentOID = arm.Commit.OID
		claim.Commit.OID = fmt.Sprintf("%040x", 880000+index)
		claim.Commit.TreeOID = fmt.Sprintf("%040x", 890000+index)
		claim.ObservedRefOID = claim.Commit.OID
		claim.Acquired = StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: claim.Commit.OID}
		entries := make([]StateV3StateTreeEntry, 0, len(arm.Tree.Entries)+1)
		for _, entry := range arm.Tree.Entries {
			if entry.Path != stateV3CoordinationArmPath {
				entries = append(entries, entry)
			}
		}
		entries = append(entries, StateV3StateTreeEntry{Path: claim.Path, Mode: "100644", Type: "blob", OID: claim.BlobOID})
		sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
		claim.Tree = StateV3StateTreeEvidence{OID: claim.Commit.TreeOID, Complete: true, Entries: entries}
		childClaims := append(append([]StateV3ReleaseLineClaimEvidence(nil), parent.Claims...), *claim)
		sort.Slice(childClaims, func(i, j int) bool { return childClaims[i].Path < childClaims[j].Path })
		child := StateV3CoordinationSnapshot{Commit: claim.Commit, Tree: claim.Tree, Claims: childClaims}
		child.Commit.ChangedPaths = stateV3TreeChanges(arm.Tree, child.Tree)
		claim.Commit.ChangedPaths = append([]StateV3ChangedPath(nil), child.Commit.ChangedPaths...)
		for i := range child.Claims {
			if child.Claims[i].Path == claim.Path {
				child.Claims[i] = *claim
			}
		}
		chronological = append(chronological, arm, child)
	}
	current := chronological[len(chronological)-1]
	predecessors := make([]StateV3CoordinationSnapshot, 0, len(chronological)-1)
	for i := len(chronological) - 2; i >= 0; i-- {
		predecessors = append(predecessors, chronological[i])
	}
	evidence.Coordination = StateV3CoordinationObservation{StateRef: policy.Coordination.StateRef, CheckpointOID: policy.Coordination.CheckpointOID, Head: current.Commit, Tree: current.Tree, Claims: current.Claims, Authentication: StateV3CoordinationAuthentication{StateRef: policy.Coordination.StateRef, CheckpointOID: policy.Coordination.CheckpointOID, HeadOID: current.Commit.OID, Current: current, Predecessors: predecessors}}
}

func fixtureStateV3CoordinationArm(t *testing.T, parent StateV3CoordinationSnapshot, path, operation, expectedClaim string, identity StateV3ReleaseLineClaim, sequence int, policy StateV3Policy) StateV3CoordinationSnapshot {
	t.Helper()
	arm := StateV3CoordinationMutationArm{Operation: operation, Ref: StateV3CoordinationRef, ClaimPath: path, RequestKey: identity.RequestKey, ReleaseLine: identity.ReleaseLine, LaneRef: identity.LaneRef, ResolvedVersion: identity.ResolvedVersion, ExpectedHeadOID: parent.Commit.OID, ExpectedClaimBlobOID: expectedClaim, Attempt: 1}
	if operation == "claim_acquire" {
		digest, ok := stateV3CanonicalDigest(identity)
		if !ok {
			t.Fatal("fixture claim intent digest")
		}
		arm.IntendedClaimSHA256 = digest
	}
	raw, err := canonicalJSON(arm)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	blob := stateV3GitBlobOID(raw)
	commit := StateV3StateCommitEvidence{OID: fmt.Sprintf("%040x", 930000+sequence), ParentOID: parent.Commit.OID, TreeOID: fmt.Sprintf("%040x", 940000+sequence), RESTVerified: true, RESTReason: StateV3RequiredRESTVerificationReason, GraphQLSignatureValid: true, WasSignedByGitHub: true, SignatureState: StateV3RequiredSignatureState, Roles: policy.CommitRoles}
	entries := append([]StateV3StateTreeEntry(nil), parent.Tree.Entries...)
	entries = append(entries, StateV3StateTreeEntry{Path: stateV3CoordinationArmPath, Mode: "100644", Type: "blob", OID: blob})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	child := StateV3CoordinationSnapshot{Commit: commit, Tree: StateV3StateTreeEvidence{OID: commit.TreeOID, Complete: true, Entries: entries}, Claims: cloneStateV3(t, parent.Claims), Arm: &StateV3CoordinationArmEvidence{Path: stateV3CoordinationArmPath, Raw: raw, SHA256: hex.EncodeToString(digest[:]), BlobOID: blob, Arm: arm}}
	child.Commit.ChangedPaths = stateV3TreeChanges(parent.Tree, child.Tree)
	return child
}

func fixtureStateV3CoordinationClaim(t *testing.T, reservation *StateV3Reservation) {
	t.Helper()
	policy := fixtureStateV3Policy()
	lane, ok := stateV3LaneForReservation(*reservation, policy)
	if !ok {
		t.Fatal("fixture lane")
	}
	path, ok := stateV3CoordinationClaimPath(reservation.ReleaseLine)
	if !ok {
		t.Fatal("fixture claim path")
	}
	// The coordination arm is a transient observation inside the record's
	// external-auth fixture, never canonical record data.
	parent := reservation.VersionResolution.Coordination
	draft := StateV3ReleaseLineClaim{ReleaseLine: reservation.ReleaseLine, RequestKey: reservation.RequestKey, RequestSHA256: reservation.RequestSHA256, LaneRef: lane.StateRef, LaneExpectedHeadOID: lane.CheckpointOID, Command: reservation.Command, ResolvedVersion: reservation.ResolvedVersion, DevelopmentVersion: reservation.DevelopmentVersion, State: "active", Phase: StateV3PhaseReserved, Attempt: 1}
	armSnapshot := fixtureStateV3CoordinationArm(t, parent.Authentication.Current, path, "claim_acquire", "", draft, len(parent.Authentication.Predecessors)+2, policy)
	armAuth := StateV3CoordinationAuthentication{StateRef: policy.Coordination.StateRef, CheckpointOID: policy.Coordination.CheckpointOID, HeadOID: armSnapshot.Commit.OID, Current: armSnapshot, Predecessors: append([]StateV3CoordinationSnapshot{parent.Authentication.Current}, parent.Authentication.Predecessors...), LaneTerminations: parent.Authentication.LaneTerminations}
	reservation.VersionResolution.Coordination = StateV3CoordinationObservation{StateRef: policy.Coordination.StateRef, CheckpointOID: policy.Coordination.CheckpointOID, Head: armSnapshot.Commit, Tree: armSnapshot.Tree, Claims: armSnapshot.Claims, Authentication: armAuth}
	reservationDigest, ok := stateV3ReservationClaimDigest(*reservation)
	if !ok {
		t.Fatal("fixture reservation digest")
	}
	resolutionDigest, ok := stateV3ResolutionDigest(reservation.VersionResolution)
	if !ok {
		t.Fatal("fixture resolution digest")
	}
	claim := StateV3ReleaseLineClaim{ReleaseLine: reservation.ReleaseLine, RequestKey: reservation.RequestKey, RequestSHA256: reservation.RequestSHA256, LaneRef: lane.StateRef, LaneExpectedHeadOID: lane.CheckpointOID, ReservationSHA256: reservationDigest, VersionResolutionSHA256: resolutionDigest, Command: reservation.Command, ResolvedVersion: reservation.ResolvedVersion, DevelopmentVersion: reservation.DevelopmentVersion, State: "active", Phase: StateV3PhaseReserved, Attempt: 1}
	raw, err := canonicalJSON(claim)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	blob := stateV3GitBlobOID(raw)
	commitOID := "ffffffffffffffffffffffffffffffffffffffff"
	treeOID := "1111111111111111111111111111111111111111"
	entries := make([]StateV3StateTreeEntry, 0, len(armSnapshot.Tree.Entries))
	for _, entry := range armSnapshot.Tree.Entries {
		if entry.Path != stateV3CoordinationArmPath {
			entries = append(entries, entry)
		}
	}
	entries = append(entries, StateV3StateTreeEntry{Path: path, Mode: "100644", Type: "blob", OID: blob})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	childTree := StateV3StateTreeEvidence{OID: treeOID, Complete: true, Entries: entries}
	commit := StateV3StateCommitEvidence{OID: commitOID, ParentOID: armSnapshot.Commit.OID, TreeOID: treeOID, RESTVerified: true, RESTReason: StateV3RequiredRESTVerificationReason, GraphQLSignatureValid: true, WasSignedByGitHub: true, SignatureState: StateV3RequiredSignatureState, Roles: policy.CommitRoles}
	commit.ChangedPaths = stateV3TreeChanges(armSnapshot.Tree, childTree)
	reservation.CoordinationClaim = StateV3ReleaseLineClaimEvidence{Path: path, Raw: raw, SHA256: hex.EncodeToString(digest[:]), BlobOID: blob, Claim: claim, Acquired: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: commitOID}, Commit: commit, Tree: childTree, ObservedRefOID: commitOID}

	// The arm precedes claim creation and commits to the complete immutable
	// claim identity. It is external authentication, so refreshing its raw
	// tree entry does not alter either claim digest.
	armSnapshot.Arm.Arm.IntendedClaimSHA256 = reservation.CoordinationClaim.SHA256
	armRaw, err := canonicalJSON(armSnapshot.Arm.Arm)
	if err != nil {
		t.Fatal(err)
	}
	armDigest := sha256.Sum256(armRaw)
	armSnapshot.Arm.Raw = armRaw
	armSnapshot.Arm.SHA256 = hex.EncodeToString(armDigest[:])
	armSnapshot.Arm.BlobOID = stateV3GitBlobOID(armRaw)
	for index := range armSnapshot.Tree.Entries {
		if armSnapshot.Tree.Entries[index].Path == stateV3CoordinationArmPath {
			armSnapshot.Tree.Entries[index].OID = armSnapshot.Arm.BlobOID
		}
	}
	armSnapshot.Commit.ChangedPaths = stateV3TreeChanges(parent.Authentication.Current.Tree, armSnapshot.Tree)
	reservation.CoordinationClaim.Commit.ChangedPaths = stateV3TreeChanges(armSnapshot.Tree, reservation.CoordinationClaim.Tree)
	reservation.VersionResolution.Coordination.Head = armSnapshot.Commit
	reservation.VersionResolution.Coordination.Tree = armSnapshot.Tree
	reservation.VersionResolution.Coordination.Claims = armSnapshot.Claims
	reservation.VersionResolution.Coordination.Authentication.Current = armSnapshot
}

func fixtureStateV3CoordinationObservation(t *testing.T) StateV3CoordinationObservation {
	t.Helper()
	policy := fixtureStateV3Policy()
	current := StateV3CoordinationSnapshot{
		Commit: StateV3StateCommitEvidence{OID: policy.Coordination.CheckpointOID, TreeOID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", ChangedPaths: []StateV3ChangedPath{}, RESTVerified: true, RESTReason: StateV3RequiredRESTVerificationReason, GraphQLSignatureValid: true, WasSignedByGitHub: true, SignatureState: StateV3RequiredSignatureState, Roles: policy.CommitRoles},
		Tree:   StateV3StateTreeEvidence{OID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Complete: true, Entries: []StateV3StateTreeEntry{}}, Claims: []StateV3ReleaseLineClaimEvidence{},
	}
	return StateV3CoordinationObservation{StateRef: policy.Coordination.StateRef, CheckpointOID: policy.Coordination.CheckpointOID, Head: current.Commit, Tree: current.Tree, Claims: current.Claims, Authentication: StateV3CoordinationAuthentication{StateRef: policy.Coordination.StateRef, CheckpointOID: policy.Coordination.CheckpointOID, HeadOID: current.Commit.OID, Current: current, Predecessors: []StateV3CoordinationSnapshot{}}}
}

func fixtureStateV3IncompleteTagDerivation(t *testing.T, reservation StateV3Reservation, version string, manifestOID string, present []string) StateV3IncompleteTagDerivation {
	t.Helper()
	expected := []string{"contrib/a/" + version, version}
	plan := map[string]any{
		"schema_version": "1", "source_sha": reservation.SourceOID, "branch": reservation.ReleaseBranch,
		"requested_version": version, "root_module": "github.com/DataDog/dd-trace-go/v2",
		"modules":                []any{map[string]any{"path": "github.com/DataDog/dd-trace-go/v2", "dir": ".", "tagged": true}},
		"permitted_output_files": []any{"internal/version/version.go"}, "expected_tags": []any{expected[0], expected[1]},
	}
	raw, err := canonicalJSON(plan)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	expectedRefs := []string{"refs/tags/" + expected[0], "refs/tags/" + expected[1]}
	sort.Strings(expectedRefs)
	sort.Strings(present)
	return StateV3IncompleteTagDerivation{Version: version, ManifestOID: manifestOID, PeeledCommitOID: reservation.SourceOID, SourceTreeOID: reservation.VersionResolution.Source.Tree.OID, RootTag: StateV3HistoricalTagEvidence{TagObjectOID: manifestOID, ObjectType: "tag", TargetType: "commit", PeeledCommitOID: reservation.SourceOID, Signature: "absent"}, HistoricalCommit: reservation.VersionResolution.Source.Commit, HistoricalTree: reservation.VersionResolution.Source.Tree, ToolPath: "scripts/autoreleasetagger", ToolSHA256: fixtureStateV3Policy().TaggerArtifact.SHA256, ToolArtifact: fixtureStateV3Policy().TaggerArtifact, Execution: StateV3PlanExecutionAttestation{SchemaVersion: "1", Collector: "strict_github_backend", ToolArtifact: fixtureStateV3Policy().TaggerArtifact, SourceCommitOID: reservation.SourceOID, SourceTreeOID: reservation.VersionResolution.Source.Tree.OID, PlanSHA256: hex.EncodeToString(digest[:]), PlanBlobOID: stateV3GitBlobOID(raw), Verified: true}, PlanRaw: raw, PlanSHA256: hex.EncodeToString(digest[:]), PlanBlobOID: stateV3GitBlobOID(raw), ExpectedTagRefs: expectedRefs, PresentTagRefs: present}
}

func fixtureStateV3StagedEnvelope(t *testing.T, record StateV3Record) *StateV3StagedEnvelopeEvidence {
	t.Helper()
	if record.Prepared == nil {
		t.Fatal("staged envelope requires prepared fixture data")
	}
	manifest := stateV3PreparedManifest{SchemaVersion: StateV3SchemaVersion, Bundle: record.Prepared.Bundle, AdditionCount: record.Prepared.AdditionCount, TotalDecodedAdditionBytes: record.Prepared.TotalDecodedAdditionBytes, ToolDigest: record.Prepared.ToolDigest, ValidatorDigest: record.Prepared.ValidatorDigest, Mutation: record.Prepared.Mutation, TagPlans: record.TagPlans}
	preparedRaw, err := canonicalJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	reservationRaw, err := canonicalJSON(record.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	bundleRaw := []byte("synthetic generated release bundle")
	raws := [][]byte{bundleRaw, preparedRaw, reservationRaw}
	envelope := &StateV3StagedEnvelopeEvidence{Prepared: *record.Prepared, TagPlans: cloneStateV3(t, record.TagPlans)}
	for index, raw := range raws {
		digest := sha256.Sum256(raw)
		envelope.Files = append(envelope.Files, StateV3StagedFile{Path: record.Prepared.StateFiles[index].Path, Raw: raw, SHA256: hex.EncodeToString(digest[:]), BlobOID: stateV3GitBlobOID(raw), SizeBytes: int64(len(raw))})
	}
	return envelope
}

func fixtureStateV3Snapshot(t *testing.T, policy StateV3Policy, record *StateV3Record, envelope *StateV3StagedEnvelopeEvidence, oid, parentOID, treeOID string) StateV3StateSnapshot {
	t.Helper()
	snapshot := StateV3StateSnapshot{
		Commit:         StateV3StateCommitEvidence{OID: oid, ParentOID: parentOID, TreeOID: treeOID, RESTVerified: true, RESTReason: StateV3RequiredRESTVerificationReason, GraphQLSignatureValid: true, WasSignedByGitHub: true, SignatureState: StateV3RequiredSignatureState, Roles: policy.CommitRoles},
		Tree:           StateV3StateTreeEvidence{OID: treeOID, Complete: true, Entries: []StateV3StateTreeEntry{}},
		StagedEnvelope: cloneStateV3(t, envelope),
	}
	if record == nil {
		return snapshot
	}
	fixtureStateV3AddLease(t, &snapshot, stateV3LeaseForReservation(record.Reservation))
	raw, err := canonicalJSON(*record)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	snapshot.RecordPresent = true
	snapshot.RecordPath = record.ActiveRecordPath()
	snapshot.RawRecord = raw
	snapshot.RecordSHA256 = hex.EncodeToString(digest[:])
	snapshot.RecordBlobOID = stateV3GitBlobOID(raw)
	snapshot.ActiveRecord = StateV3ActiveRecordEvidence{Present: true, Path: snapshot.RecordPath, Raw: append([]byte(nil), raw...), SHA256: snapshot.RecordSHA256, BlobOID: snapshot.RecordBlobOID, StagedEnvelope: cloneStateV3(t, envelope)}
	snapshot.Tree.Entries = append(snapshot.Tree.Entries, StateV3StateTreeEntry{Path: snapshot.RecordPath, Mode: "100644", Type: "blob", OID: snapshot.RecordBlobOID})
	if envelope != nil {
		for _, file := range envelope.Files {
			snapshot.Tree.Entries = append(snapshot.Tree.Entries, StateV3StateTreeEntry{Path: file.Path, Mode: "100644", Type: "blob", OID: file.BlobOID})
		}
	}
	sort.Slice(snapshot.Tree.Entries, func(i, j int) bool { return snapshot.Tree.Entries[i].Path < snapshot.Tree.Entries[j].Path })
	return snapshot
}

func fixtureStateV3AddLease(t *testing.T, snapshot *StateV3StateSnapshot, lease StateV3ActiveOperationLease) {
	t.Helper()
	raw, err := canonicalJSON(lease)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	snapshot.LeasePresent = true
	snapshot.LeasePath = StateV3ActiveLeasePath
	snapshot.RawLease = raw
	snapshot.LeaseSHA256 = hex.EncodeToString(digest[:])
	snapshot.LeaseBlobOID = stateV3GitBlobOID(raw)
	snapshot.Tree.Entries = append(snapshot.Tree.Entries, StateV3StateTreeEntry{Path: StateV3ActiveLeasePath, Mode: "100644", Type: "blob", OID: snapshot.LeaseBlobOID})
	sort.Slice(snapshot.Tree.Entries, func(i, j int) bool { return snapshot.Tree.Entries[i].Path < snapshot.Tree.Entries[j].Path })
}

func fixtureStateV3RemoveLease(snapshot *StateV3StateSnapshot) {
	snapshot.LeasePresent = false
	snapshot.LeasePath, snapshot.LeaseSHA256, snapshot.LeaseBlobOID = "", "", ""
	snapshot.RawLease = nil
	for index, entry := range snapshot.Tree.Entries {
		if entry.Path == StateV3ActiveLeasePath {
			snapshot.Tree.Entries = append(snapshot.Tree.Entries[:index], snapshot.Tree.Entries[index+1:]...)
			break
		}
	}
}

func fixtureStateV3RecordAtEventCount(t *testing.T, full StateV3Record, count int) StateV3Record {
	t.Helper()
	record := cloneStateV3(t, full)
	record.Events = record.Events[:count]
	record.Phase = StateV3PhaseReserved
	record.Prepared, record.Commit = nil, nil
	record.MutationArms, record.BranchEvidence = nil, nil
	record.TagPlans, record.TagIntents, record.TagObjectEvidence, record.TagEvidence = nil, nil, nil, nil
	record.TestEvidence, record.PreparePRIntent, record.PreparePREvidence, record.OutcomeEvidence = nil, nil, nil, nil
	publishedBranches, tagObjects, publishedTags, armed := 0, 0, 0, 0
	for _, event := range record.Events {
		switch event.Kind {
		case StateV3EventMutationArmed:
			armed++
			record.MutationArms = cloneStateV3(t, full.MutationArms[:armed])
			if record.MutationArms[armed-1].OperationKind == "prepare_pr_create" {
				record.PreparePRIntent = cloneStateV3(t, full.PreparePRIntent)
			}
		case StateV3EventPhaseAdvanced:
			record.Phase = event.Phase
			if event.Phase == StateV3PhasePrepared {
				record.Prepared, record.TagPlans = cloneStateV3(t, full.Prepared), cloneStateV3(t, full.TagPlans)
			}
		case StateV3EventPlatformCommitAdopted:
			record.Commit, record.TagIntents = cloneStateV3(t, full.Commit), cloneStateV3(t, full.TagIntents)
		case StateV3EventBranchPublished:
			publishedBranches++
			record.BranchEvidence = cloneStateV3(t, full.BranchEvidence[:publishedBranches])
		case StateV3EventTestsPassed:
			record.TestEvidence = cloneStateV3(t, full.TestEvidence)
		case StateV3EventTagObjectCreated:
			tagObjects++
			record.TagObjectEvidence = cloneStateV3(t, full.TagObjectEvidence[:tagObjects])
		case StateV3EventTagPublished:
			publishedTags++
			record.TagEvidence = cloneStateV3(t, full.TagEvidence[:publishedTags])
		case StateV3EventPreparePRRecorded:
			record.PreparePRIntent = cloneStateV3(t, full.PreparePRIntent)
			record.PreparePREvidence = cloneStateV3(t, full.PreparePREvidence)
		case StateV3EventOutcomeRecorded:
			record.OutcomeEvidence = cloneStateV3(t, full.OutcomeEvidence)
		}
	}
	return record
}

func fixtureStateV3Authentication(t *testing.T, policy StateV3Policy, record StateV3Record) StateV3Authentication {
	t.Helper()
	full := cloneStateV3(t, record)
	lane, ok := stateV3LaneForReservation(record.Reservation, policy)
	if !ok {
		t.Fatal("fixture lane not derived")
	}
	reserved := fixtureStateV3RecordAtEventCount(t, full, 1)
	chronological := []StateV3StateSnapshot{fixtureStateV3Snapshot(t, policy, nil, nil, lane.CheckpointOID, "", v3OIDd)}
	leaseOnly := fixtureStateV3Snapshot(t, policy, nil, nil, fmt.Sprintf("%040x", 101), lane.CheckpointOID, fmt.Sprintf("%040x", 10001))
	fixtureStateV3AddLease(t, &leaseOnly, stateV3LeaseForReservation(full.Reservation))
	chronological = append(chronological, leaseOnly)
	chronological = append(chronological, fixtureStateV3Snapshot(t, policy, &reserved, nil, fmt.Sprintf("%040x", 102), chronological[len(chronological)-1].Commit.OID, fmt.Sprintf("%040x", 10002)))
	if len(record.Events) > 1 {
		envelope := fixtureStateV3StagedEnvelope(t, full)
		index := len(chronological)
		chronological = append(chronological, fixtureStateV3Snapshot(t, policy, &reserved, envelope, fmt.Sprintf("%040x", 100+index), chronological[index-1].Commit.OID, fmt.Sprintf("%040x", 10000+index)))
		for count := 2; count <= len(record.Events); count++ {
			prefix := fixtureStateV3RecordAtEventCount(t, full, count)
			index = len(chronological)
			chronological = append(chronological, fixtureStateV3Snapshot(t, policy, &prefix, envelope, fmt.Sprintf("%040x", 100+index), chronological[index-1].Commit.OID, fmt.Sprintf("%040x", 10000+index)))
		}
	}
	if record.Phase == StateV3PhaseComplete {
		index := len(chronological)
		released := fixtureStateV3Snapshot(t, policy, &record, chronological[index-1].StagedEnvelope, fmt.Sprintf("%040x", 100+index), chronological[index-1].Commit.OID, fmt.Sprintf("%040x", 10000+index))
		fixtureStateV3RemoveLease(&released)
		chronological = append(chronological, released)
	}
	for index := 1; index < len(chronological); index++ {
		chronological[index].Commit.ChangedPaths = stateV3TreeChanges(chronological[index-1].Tree, chronological[index].Tree)
	}
	current := chronological[len(chronological)-1]
	predecessors := make([]StateV3StateSnapshot, 0, len(chronological)-1)
	for index := len(chronological) - 2; index >= 0; index-- {
		predecessors = append(predecessors, chronological[index])
	}
	coordination := record.Reservation.VersionResolution.Coordination.Authentication
	return StateV3Authentication{StateRef: lane.StateRef, HeadOID: current.Commit.OID, CheckpointOID: lane.CheckpointOID, Current: current, Predecessors: predecessors, Coordination: &coordination}
}

func fixtureStateV3ReservedForComment(t *testing.T, commentID string) StateV3Record {
	record := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	record.Reservation.OriginalCommentID = commentID
	record.Reservation.RequestKey = "123:" + commentID
	record.Reservation.Marker = Marker("123", commentID, record.Reservation.Command, record.Reservation.RequestedVersion)
	context := Context{RepositoryID: "123", RepositoryFullName: RepositoryFullName, IssueNumber: "456", OriginalCommentID: commentID, AcknowledgementCommentID: "790", BodySnapshot: record.Reservation.BodySnapshot, PolicyRevision: record.Reservation.PolicyRevision}
	record.Reservation.RequestSHA256 = RequestSHA256(context, record.Reservation.Command, record.Reservation.RequestedVersion)
	record.Binding.RequestKey = record.Reservation.RequestKey
	record.Binding.RequestSHA256 = record.Reservation.RequestSHA256
	fixtureStateV3VersionResolution(t, &record.Reservation, "v2.11.0-dev")
	rebindStateV3Events(t, &record)
	return record
}

func fixtureStateV3BindPriorLaneHead(t *testing.T, policy StateV3Policy, prior StateV3Authentication, record *StateV3Record) {
	t.Helper()
	lane, ok := stateV3LaneForReservation(record.Reservation, policy)
	if !ok || prior.StateRef != lane.StateRef {
		t.Fatal("prior history does not match record lane")
	}
	claim := &record.Reservation.CoordinationClaim
	claim.Claim.LaneExpectedHeadOID = prior.HeadOID
	raw, err := canonicalJSON(claim.Claim)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	claim.Raw, claim.SHA256, claim.BlobOID = raw, hex.EncodeToString(digest[:]), stateV3GitBlobOID(raw)
	for index := range claim.Tree.Entries {
		if claim.Tree.Entries[index].Path == claim.Path {
			claim.Tree.Entries[index].OID = claim.BlobOID
		}
	}
	claim.Commit.ChangedPaths = stateV3TreeChanges(record.Reservation.VersionResolution.Coordination.Tree, claim.Tree)
	// The changed lane expected head changes the immutable claim digest. Refresh
	// the immediately preceding acquisition arm to bind that exact new claim.
	authentication := &record.Reservation.VersionResolution.Coordination.Authentication
	if authentication.Current.Arm == nil || authentication.Current.Arm.Arm.Operation != "claim_acquire" || len(authentication.Predecessors) == 0 {
		t.Fatal("fixture coordination acquisition arm unavailable")
	}
	authentication.Current.Arm.Arm.IntendedClaimSHA256 = claim.SHA256
	armRaw, marshalErr := canonicalJSON(authentication.Current.Arm.Arm)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	armDigest := sha256.Sum256(armRaw)
	authentication.Current.Arm.Raw = armRaw
	authentication.Current.Arm.SHA256 = hex.EncodeToString(armDigest[:])
	authentication.Current.Arm.BlobOID = stateV3GitBlobOID(armRaw)
	for index := range authentication.Current.Tree.Entries {
		if authentication.Current.Tree.Entries[index].Path == stateV3CoordinationArmPath {
			authentication.Current.Tree.Entries[index].OID = authentication.Current.Arm.BlobOID
		}
	}
	authentication.Current.Commit.ChangedPaths = stateV3TreeChanges(authentication.Predecessors[0].Tree, authentication.Current.Tree)
	claim.Commit.ChangedPaths = stateV3TreeChanges(authentication.Current.Tree, claim.Tree)
	record.Reservation.VersionResolution.Coordination.Head = authentication.Current.Commit
	record.Reservation.VersionResolution.Coordination.Tree = authentication.Current.Tree
	record.Reservation.VersionResolution.Coordination.Claims = authentication.Current.Claims
	rebindStateV3Events(t, record)
}

func fixtureStateV3AfterHistory(t *testing.T, policy StateV3Policy, prior StateV3Authentication, record StateV3Record, leaseOnly bool) StateV3Authentication {
	t.Helper()
	lane, ok := stateV3LaneForReservation(record.Reservation, policy)
	if !ok || prior.StateRef != lane.StateRef {
		t.Fatal("prior history does not match record lane")
	}
	priorSnapshots := append([]StateV3StateSnapshot{cloneStateV3(t, prior.Current)}, cloneStateV3(t, prior.Predecessors)...)
	for index := range priorSnapshots {
		priorSnapshots[index].RecordPresent = false
		priorSnapshots[index].RecordPath, priorSnapshots[index].RecordSHA256, priorSnapshots[index].RecordBlobOID = "", "", ""
		priorSnapshots[index].RawRecord = nil
		priorSnapshots[index].StagedEnvelope = nil
	}
	parent := priorSnapshots[0]
	leaseSnapshot := cloneStateV3(t, parent)
	leaseSnapshot.ActiveRecord = StateV3ActiveRecordEvidence{}
	leaseSnapshot.Commit.OID = fmt.Sprintf("%040x", 970001)
	leaseSnapshot.Commit.ParentOID = parent.Commit.OID
	leaseSnapshot.Commit.TreeOID = fmt.Sprintf("%040x", 970002)
	leaseSnapshot.Tree.OID = leaseSnapshot.Commit.TreeOID
	fixtureStateV3AddLease(t, &leaseSnapshot, stateV3LeaseForReservation(record.Reservation))
	leaseSnapshot.Commit.ChangedPaths = stateV3TreeChanges(parent.Tree, leaseSnapshot.Tree)
	current := leaseSnapshot
	if !leaseOnly {
		current = cloneStateV3(t, leaseSnapshot)
		current.Commit.OID = fmt.Sprintf("%040x", 970003)
		current.Commit.ParentOID = leaseSnapshot.Commit.OID
		current.Commit.TreeOID = fmt.Sprintf("%040x", 970004)
		current.Tree.OID = current.Commit.TreeOID
		raw, _ := canonicalJSON(record)
		digest := sha256.Sum256(raw)
		current.RecordPresent = true
		current.RecordPath = record.ActiveRecordPath()
		current.RawRecord = raw
		current.RecordSHA256 = hex.EncodeToString(digest[:])
		current.RecordBlobOID = stateV3GitBlobOID(raw)
		current.ActiveRecord = StateV3ActiveRecordEvidence{Present: true, Path: current.RecordPath, Raw: append([]byte(nil), raw...), SHA256: current.RecordSHA256, BlobOID: current.RecordBlobOID}
		current.Tree.Entries = append(current.Tree.Entries, StateV3StateTreeEntry{Path: current.RecordPath, Mode: "100644", Type: "blob", OID: current.RecordBlobOID})
		sort.Slice(current.Tree.Entries, func(i, j int) bool { return current.Tree.Entries[i].Path < current.Tree.Entries[j].Path })
		current.Commit.ChangedPaths = stateV3TreeChanges(leaseSnapshot.Tree, current.Tree)
	}
	predecessors := []StateV3StateSnapshot{}
	if !leaseOnly {
		predecessors = append(predecessors, leaseSnapshot)
	}
	predecessors = append(predecessors, priorSnapshots...)
	coordination := record.Reservation.VersionResolution.Coordination.Authentication
	return StateV3Authentication{StateRef: lane.StateRef, HeadOID: current.Commit.OID, CheckpointOID: lane.CheckpointOID, Current: current, Predecessors: predecessors, Coordination: &coordination}
}

func finalizeStateV3PreparedFixture(t *testing.T, prepared *StateV3PreparedState, reservation StateV3Reservation, plans []StateV3TagPlan) {
	t.Helper()
	bundleRaw := []byte("synthetic generated release bundle")
	bundleDigest := sha256.Sum256(bundleRaw)
	prepared.Bundle.SHA256 = hex.EncodeToString(bundleDigest[:])
	prepared.Bundle.BlobOID = stateV3GitBlobOID(bundleRaw)
	prepared.Bundle.SizeBytes = int64(len(bundleRaw))
	reservationRaw, err := canonicalJSON(reservation)
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 10; iteration++ {
		manifest := stateV3PreparedManifest{SchemaVersion: StateV3SchemaVersion, Bundle: prepared.Bundle, AdditionCount: prepared.AdditionCount, TotalDecodedAdditionBytes: prepared.TotalDecodedAdditionBytes, ToolDigest: prepared.ToolDigest, ValidatorDigest: prepared.ValidatorDigest, Mutation: prepared.Mutation, TagPlans: plans}
		preparedRaw, marshalErr := canonicalJSON(manifest)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		total := int64(len(bundleRaw) + len(preparedRaw) + len(reservationRaw))
		if total == prepared.TotalDecodedAdditionBytes {
			raws := [][]byte{bundleRaw, preparedRaw, reservationRaw}
			for index, raw := range raws {
				digest := sha256.Sum256(raw)
				prepared.StateFiles[index].SHA256 = hex.EncodeToString(digest[:])
				prepared.StateFiles[index].BlobOID = stateV3GitBlobOID(raw)
				prepared.StateFiles[index].SizeBytes = int64(len(raw))
			}
			return
		}
		prepared.TotalDecodedAdditionBytes = total
	}
	t.Fatal("prepared fixture size did not converge")
}

func fixtureStateV3Record(t *testing.T) StateV3Record {
	t.Helper()
	policy := fixtureStateV3Policy()
	changes := []StateV3FileChange{
		{Path: "go.mod", Operation: "addition", Mode: "100644", BlobOID: v3OIDa, SHA256: v3SHAa, SizeBytes: 120},
		{Path: "version.go", Operation: "addition", Mode: "100644", BlobOID: v3OIDb, SHA256: v3SHAb, SizeBytes: 80},
	}
	changesDigest, err := stateV3FileChangesDigest(changes)
	if err != nil {
		t.Fatal(err)
	}
	requestContext := Context{
		RepositoryID: "123", RepositoryFullName: RepositoryFullName, IssueNumber: "456",
		OriginalCommentID: "789", AcknowledgementCommentID: "790",
		BodySnapshot: "/gardener release:promote v2.11.0", PolicyRevision: policy.PolicyRevision,
	}
	reservation := StateV3Reservation{
		ContractVersion: ContractVersion, RequestKey: "123:789",
		RequestSHA256: RequestSHA256(requestContext, "release:promote", "v2.11.0"),
		RepositoryID:  "123", RepositoryFullName: RepositoryFullName, IssueNumber: "456",
		OriginalCommentID: "789", AcknowledgementCommentID: "790",
		BodySnapshot:     requestContext.BodySnapshot,
		Marker:           Marker("123", "789", "release:promote", "v2.11.0"),
		ValidatedActorID: "42", ValidatedActorLogin: "release-maintainer",
		Command: "release:promote", RequestedVersion: "v2.11.0", ResolvedVersion: "v2.11.0-rc.1",
		ReleaseLine: "v2.11", GenerationVersion: "v2.11.0-rc.1",
		SourceRef: "refs/heads/release-v2.11.x", SourceOID: v3OIDa, PolicyRevision: policy.PolicyRevision,
	}
	fixtureStateV3VersionResolution(t, &reservation, "v2.11.0-dev")
	mutation := StateV3CommitMutationIntent{
		RepositoryID: "123", RepositoryFullName: RepositoryFullName,
		TargetRef: "refs/heads/release-v2.11.x", ExpectedHeadOID: v3OIDa,
		ExpectedTreeOID: v3OIDb, Message: "release: v2.11.0-rc.1",
		FileChangesSHA256: changesDigest, FileChanges: changes,
		Branches: []StateV3BranchMutationIntent{{Ref: "refs/heads/release-v2.11.x", ExpectedOldOID: v3OIDa, Target: "platform_commit"}},
	}
	prepared := StateV3PreparedState{
		Bundle: StateV3BundleIdentity{
			Path: "requests/123/789/generation.bundle", SHA256: v3SHAa,
			BlobOID: v3OIDa, SizeBytes: MaxStateV3PreparedBundleBytes, PrerequisiteOID: v3OIDa,
		},
		StateFiles: []StateV3PreparedFile{
			{Path: "requests/123/789/generation.bundle", BlobOID: v3OIDa, SHA256: v3SHAa, SizeBytes: 1_048_576},
			{Path: "requests/123/789/prepared.json", BlobOID: v3OIDb, SHA256: v3SHAb, SizeBytes: 303},
			{Path: "requests/123/789/reservation.json", BlobOID: v3OIDc, SHA256: v3SHAc, SizeBytes: 306},
		},
		AdditionCount: StateV3PreparedAdditionCount, TotalDecodedAdditionBytes: MaxStateV3DecodedAdditionBytes,
		ToolDigest: v3SHAa, ValidatorDigest: v3SHAb,
		Mutation: mutation,
	}
	commit := StateV3AdoptedCommit{
		OID: v3OIDc, MutationResponse: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: v3OIDc},
		ObservedRefOID: v3OIDc, RESTOID: v3OIDc, GraphQLOID: v3OIDc,
		Ref: mutation.TargetRef, ParentOID: mutation.ExpectedHeadOID, TreeOID: mutation.ExpectedTreeOID,
		Message: mutation.Message, FileChangesSHA256: mutation.FileChangesSHA256, FileChanges: cloneStateV3(t, mutation.FileChanges),
		RESTVerified: true, RESTReason: StateV3RequiredRESTVerificationReason,
		GraphQLSignatureValid: true, WasSignedByGitHub: true, SignatureState: StateV3RequiredSignatureState,
		Roles: policy.CommitRoles,
	}
	tagPlans := []StateV3TagPlan{{
		Name: "v2.11.0-rc.1", Ref: "refs/tags/v2.11.0-rc.1", Message: "v2.11.0-rc.1\n",
		TargetRef: mutation.TargetRef, Tagger: policy.Tagger,
		TaggerDate: "2026-09-12T06:30:00Z", Signature: "absent",
	}}
	finalizeStateV3PreparedFixture(t, &prepared, reservation, tagPlans)
	tagObjectOID, ok := stateV3TagObjectOID(tagPlans[0], commit.OID)
	if !ok {
		t.Fatal("could not calculate fixture tag object OID")
	}
	if tagObjectOID != "6dc893250ad5759ca5ca82d3f0afe36752857457" {
		t.Fatalf("deterministic tag object OID = %q", tagObjectOID)
	}
	tagIntents := []StateV3TagIntent{{
		Name: tagPlans[0].Name, Ref: tagPlans[0].Ref, Message: tagPlans[0].Message,
		ObjectType: "tag", TargetType: "commit", TargetOID: commit.OID,
		ExpectedTagObjectOID: tagObjectOID, Tagger: tagPlans[0].Tagger,
		TaggerDate: tagPlans[0].TaggerDate, Signature: "absent",
	}}
	tagObjectEvidence := []StateV3TagObjectEvidence{{Intent: tagIntents[0], Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: tagObjectOID}, RESTTagObjectOID: tagObjectOID, PeeledCommitOID: commit.OID}}
	tagEvidence := []StateV3TagEvidence{{
		Name: tagIntents[0].Name, Ref: tagIntents[0].Ref, TagObjectOID: tagObjectOID,
		RefResponse:    StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: tagObjectOID},
		ObservedRefOID: tagObjectOID, RESTTagObjectOID: tagObjectOID,
		Message: tagIntents[0].Message, ObjectType: "tag", TargetType: "commit", TargetOID: commit.OID,
		PeeledCommitOID: commit.OID, Signature: "absent", Tagger: policy.Tagger,
		TaggerDate: tagIntents[0].TaggerDate,
	}}
	testEvidence := &StateV3TestEvidence{
		SchemaVersion: "1", Repository: RepositoryFullName,
		WorkflowPath: policy.Test.WorkflowPath, WorkflowID: policy.Test.WorkflowID,
		WorkflowSHA256: policy.Test.WorkflowSHA256, Event: policy.Test.Event,
		ReleaseOID: commit.OID, RequiredJobs: cloneStateV3(t, policy.Test.RequiredJobs),
		Runs: []StateV3TestRunEvidence{{
			TargetBranch: "release-v2.11.x", TargetOID: commit.OID, RunID: "80001", Attempt: 1,
			Status: "completed", Conclusion: "success",
			Jobs: []StateV3TestJobEvidence{
				{ID: "81001", Name: "required-a", Attempt: 1, Status: "completed", Conclusion: "success"},
				{ID: "81002", Name: "required-b", Attempt: 1, Status: "completed", Conclusion: "success"},
			},
		}},
	}
	outcome := &StateV3OutcomeEvidence{
		SchemaVersion: "1", RequestKey: reservation.RequestKey, Command: reservation.Command,
		ReleaseOID: commit.OID, Publication: WorkSucceeded,
		PreparePR: WorkNotApplicable, Images: WorkNotApplicable, ImageEvidence: []StateV3ImageEvidence{},
	}
	record := StateV3Record{
		SchemaVersion: StateV3SchemaVersion, Reservation: reservation,
		Binding: StateV3ExecutionBinding{
			RequestKey: reservation.RequestKey, RequestSHA256: reservation.RequestSHA256,
			WorkflowSHA: v3OIDd, WorkflowRef: "refs/heads/main", WorkflowPath: StateV3WorkflowPath,
			WorkflowFileSHA256: v3SHAc, WorkflowRunID: "12345", WorkflowRunAttempt: 1,
			PolicyRevision: reservation.PolicyRevision,
		},
		Phase: StateV3PhaseComplete, Prepared: &prepared, Commit: &commit,
		BranchEvidence: []StateV3BranchEvidence{{Intent: mutation.Branches[0], Response: commit.MutationResponse, ObjectOID: commit.OID, ObservedRefOID: commit.OID, Status: "present", Disposition: "published"}},
		TagPlans:       tagPlans, TagIntents: tagIntents, TagObjectEvidence: tagObjectEvidence, TagEvidence: tagEvidence,
		TestEvidence: testEvidence, OutcomeEvidence: outcome,
	}
	record.OutcomeEvidence.PriorStateSHA256, _ = stateV3OutcomePriorStateDigest(record)
	appendEvent := func(event StateV3Event) {
		event.EvidenceSHA256, _ = stateV3ExpectedEventEvidenceDigest(record, event, len(record.Events))
		record.Events, err = appendStateV3Event(record.Events, event)
		if err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(StateV3Event{Kind: StateV3EventReserved, Phase: StateV3PhaseReserved})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhasePrepared})
	commitArm, _ := stateV3MutationArm("platform_commit", mutation.TargetRef, mutation.ExpectedHeadOID, "", false, mutation)
	record.MutationArms = append(record.MutationArms, commitArm)
	appendEvent(StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhasePrepared, Ref: commitArm.Ref, ExpectedOldOID: commitArm.ExpectedOldOID, Disposition: "armed"})
	appendEvent(StateV3Event{Kind: StateV3EventPlatformCommitAdopted, Phase: StateV3PhasePrepared, Ref: commit.Ref, ExpectedOldOID: commit.ParentOID, ObjectOID: commit.OID, Disposition: "adopted"})
	appendEvent(StateV3Event{Kind: StateV3EventBranchPublished, Phase: StateV3PhasePrepared, Ref: commit.Ref, ExpectedOldOID: commit.ParentOID, ObjectOID: commit.OID, Disposition: "published"})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhaseBranchesPublished})
	appendEvent(StateV3Event{Kind: StateV3EventTestsPassed, Phase: StateV3PhaseBranchesPublished})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhaseTestsPassed})
	objectArm, _ := stateV3MutationArm("tag_object_create", tagIntents[0].Ref, "", tagIntents[0].ExpectedTagObjectOID, true, tagIntents[0])
	record.MutationArms = append(record.MutationArms, objectArm)
	appendEvent(StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhaseTestsPassed, Ref: objectArm.Ref, ObjectOID: objectArm.IntendedObjectOID, Disposition: "armed"})
	appendEvent(StateV3Event{Kind: StateV3EventTagObjectCreated, Phase: StateV3PhaseTestsPassed, Ref: tagIntents[0].Ref, ObjectOID: tagIntents[0].ExpectedTagObjectOID, Disposition: "created"})
	refIntent := struct {
		Ref       string `json:"ref"`
		ObjectOID string `json:"object_oid"`
	}{tagIntents[0].Ref, tagIntents[0].ExpectedTagObjectOID}
	refArm, _ := stateV3MutationArm("tag_ref_create", tagIntents[0].Ref, "", tagIntents[0].ExpectedTagObjectOID, true, refIntent)
	record.MutationArms = append(record.MutationArms, refArm)
	appendEvent(StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhaseTestsPassed, Ref: refArm.Ref, ObjectOID: refArm.IntendedObjectOID, Disposition: "armed"})
	appendEvent(StateV3Event{Kind: StateV3EventTagPublished, Phase: StateV3PhaseTestsPassed, Ref: tagEvidence[0].Ref, ObjectOID: tagEvidence[0].TagObjectOID, Disposition: "published"})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhaseTagsPublished})
	record.OutcomeEvidence.PriorStateSHA256, _ = stateV3OutcomePriorStateDigest(record)
	appendEvent(StateV3Event{Kind: StateV3EventOutcomeRecorded, Phase: StateV3PhaseTagsPublished})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhaseComplete})
	return record
}

func fixtureReleaseStateV3Record(t *testing.T) StateV3Record {
	t.Helper()
	record := fixtureStateV3Record(t)
	policy := fixtureStateV3Policy()
	record.Reservation.Command = "release:release"
	record.Reservation.RequestedVersion = "v2.11.0"
	record.Reservation.ResolvedVersion = "v2.11.0"
	record.Reservation.GenerationVersion = "v2.11.0"
	record.Reservation.BodySnapshot = "/gardener release:release v2.11.0"
	record.Reservation.Marker = Marker("123", "789", "release:release", "v2.11.0")
	context := Context{RepositoryID: "123", RepositoryFullName: RepositoryFullName, IssueNumber: "456", OriginalCommentID: "789", AcknowledgementCommentID: "790", BodySnapshot: record.Reservation.BodySnapshot, PolicyRevision: record.Reservation.PolicyRevision}
	record.Reservation.RequestSHA256 = RequestSHA256(context, record.Reservation.Command, record.Reservation.RequestedVersion)
	record.Binding.RequestSHA256 = record.Reservation.RequestSHA256
	fixtureStateV3VersionResolution(t, &record.Reservation, "v2.11.0-rc.1")
	record.Prepared.Mutation.Message = "release: v2.11.0"
	record.Commit.Message = record.Prepared.Mutation.Message
	names := []string{"v2.11.0"}
	for _, item := range ImagePackagePolicy() {
		names = append(names, item.ModulePrefix+"v2.11.0")
	}
	sort.Strings(names)
	record.MutationArms = nil
	record.TagPlans, record.TagIntents, record.TagObjectEvidence, record.TagEvidence = nil, nil, nil, nil
	for index, name := range names {
		plan := StateV3TagPlan{Name: name, Ref: "refs/tags/" + name, Message: name + "\n", TargetRef: record.Commit.Ref, Tagger: policy.Tagger, TaggerDate: fmt.Sprintf("2026-09-12T06:%02d:00Z", 30+index), Signature: "absent"}
		oid, ok := stateV3TagObjectOID(plan, record.Commit.OID)
		if !ok {
			t.Fatal("could not calculate release tag OID")
		}
		intent := StateV3TagIntent{Name: name, Ref: plan.Ref, Message: plan.Message, ObjectType: "tag", TargetType: "commit", TargetOID: record.Commit.OID, ExpectedTagObjectOID: oid, Tagger: plan.Tagger, TaggerDate: plan.TaggerDate, Signature: "absent"}
		objectEvidence := StateV3TagObjectEvidence{Intent: intent, Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: oid}, RESTTagObjectOID: oid, PeeledCommitOID: record.Commit.OID}
		evidence := StateV3TagEvidence{Name: name, Ref: plan.Ref, TagObjectOID: oid, RefResponse: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: oid}, ObservedRefOID: oid, RESTTagObjectOID: oid, Message: plan.Message, ObjectType: "tag", TargetType: "commit", TargetOID: record.Commit.OID, PeeledCommitOID: record.Commit.OID, Signature: "absent", Tagger: plan.Tagger, TaggerDate: plan.TaggerDate}
		record.TagPlans = append(record.TagPlans, plan)
		record.TagIntents = append(record.TagIntents, intent)
		record.TagObjectEvidence = append(record.TagObjectEvidence, objectEvidence)
		record.TagEvidence = append(record.TagEvidence, evidence)
	}
	finalizeStateV3PreparedFixture(t, record.Prepared, record.Reservation, record.TagPlans)
	record.OutcomeEvidence.Command = record.Reservation.Command
	record.OutcomeEvidence.PreparePR = WorkNotApplicable
	record.OutcomeEvidence.Images = WorkSucceeded
	record.OutcomeEvidence.ImageEvidence = nil
	root := findStateV3TagEvidenceForTest(t, record.TagEvidence, "refs/tags/v2.11.0")
	for index, item := range ImagePackagePolicy() {
		module := findStateV3TagEvidenceForTest(t, record.TagEvidence, "refs/tags/"+item.ModulePrefix+"v2.11.0")
		record.OutcomeEvidence.ImageEvidence = append(record.OutcomeEvidence.ImageEvidence, StateV3ImageEvidence{WorkflowID: policy.Image.WorkflowID, Detail: ImagePromotionEvidence{SchemaVersion: "1", Outcome: ImagePromoted, RepositoryFullName: RepositoryFullName, WorkflowPath: policy.Image.WorkflowPath, WorkflowSHA256: policy.Image.WorkflowSHA256, ChildWorkflowSHA256: policy.Image.ChildWorkflowSHA256, RunID: fmt.Sprintf("9%04d", index+1), RunAttempt: 1, Event: "push", BuildStatus: "completed", BuildConclusion: "success", ModuleTagRef: module.Ref, ModuleTagObjectSHA: module.TagObjectOID, CommitSHA: record.Commit.OID, Version: "v2.11.0", Image: item.Image, VersionDigest: "sha256:" + v3SHAa, RootTagObjectSHA: root.TagObjectOID}})
	}
	record.OutcomeEvidence.PriorStateSHA256, _ = stateV3OutcomePriorStateDigest(record)
	record.Events = nil
	appendEvent := func(event StateV3Event) {
		event.EvidenceSHA256, _ = stateV3ExpectedEventEvidenceDigest(record, event, len(record.Events))
		var err error
		record.Events, err = appendStateV3Event(record.Events, event)
		if err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(StateV3Event{Kind: StateV3EventReserved, Phase: StateV3PhaseReserved})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhasePrepared})
	commitArm, _ := stateV3MutationArm("platform_commit", record.Prepared.Mutation.TargetRef, record.Prepared.Mutation.ExpectedHeadOID, "", false, record.Prepared.Mutation)
	record.MutationArms = append(record.MutationArms, commitArm)
	appendEvent(StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhasePrepared, Ref: commitArm.Ref, ExpectedOldOID: commitArm.ExpectedOldOID, Disposition: "armed"})
	appendEvent(StateV3Event{Kind: StateV3EventPlatformCommitAdopted, Phase: StateV3PhasePrepared, Ref: record.Commit.Ref, ExpectedOldOID: record.Commit.ParentOID, ObjectOID: record.Commit.OID, Disposition: "adopted"})
	appendEvent(StateV3Event{Kind: StateV3EventBranchPublished, Phase: StateV3PhasePrepared, Ref: record.Commit.Ref, ExpectedOldOID: record.Commit.ParentOID, ObjectOID: record.Commit.OID, Disposition: "published"})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhaseBranchesPublished})
	appendEvent(StateV3Event{Kind: StateV3EventTestsPassed, Phase: StateV3PhaseBranchesPublished})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhaseTestsPassed})
	for index, tag := range record.TagEvidence {
		intent := record.TagIntents[index]
		objectArm, _ := stateV3MutationArm("tag_object_create", intent.Ref, "", intent.ExpectedTagObjectOID, true, intent)
		record.MutationArms = append(record.MutationArms, objectArm)
		appendEvent(StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhaseTestsPassed, Ref: objectArm.Ref, ObjectOID: objectArm.IntendedObjectOID, Disposition: "armed"})
		appendEvent(StateV3Event{Kind: StateV3EventTagObjectCreated, Phase: StateV3PhaseTestsPassed, Ref: intent.Ref, ObjectOID: intent.ExpectedTagObjectOID, Disposition: "created"})
		refIntent := struct {
			Ref       string `json:"ref"`
			ObjectOID string `json:"object_oid"`
		}{intent.Ref, intent.ExpectedTagObjectOID}
		refArm, _ := stateV3MutationArm("tag_ref_create", intent.Ref, "", intent.ExpectedTagObjectOID, true, refIntent)
		record.MutationArms = append(record.MutationArms, refArm)
		appendEvent(StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhaseTestsPassed, Ref: refArm.Ref, ObjectOID: refArm.IntendedObjectOID, Disposition: "armed"})
		appendEvent(StateV3Event{Kind: StateV3EventTagPublished, Phase: StateV3PhaseTestsPassed, Ref: tag.Ref, ObjectOID: tag.TagObjectOID, Disposition: "published"})
	}
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhaseTagsPublished})
	record.OutcomeEvidence.PriorStateSHA256, _ = stateV3OutcomePriorStateDigest(record)
	appendEvent(StateV3Event{Kind: StateV3EventOutcomeRecorded, Phase: StateV3PhaseTagsPublished})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhaseComplete})
	return record
}

func findStateV3TagEvidenceForTest(t *testing.T, tags []StateV3TagEvidence, ref string) StateV3TagEvidence {
	t.Helper()
	for _, tag := range tags {
		if tag.Ref == ref {
			return tag
		}
	}
	t.Fatalf("missing fixture tag %s", ref)
	return StateV3TagEvidence{}
}

func cloneStateV3[T any](t *testing.T, value T) T {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone T
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	// External coordination authentication is intentionally excluded from
	// canonical record JSON. Preserve it across fixture cloning so tests pass
	// it through the transient validation seam.
	if original, ok := any(value).(StateV3Record); ok {
		if copied, ok := any(clone).(StateV3Record); ok {
			copied.Reservation.VersionResolution.Coordination.Authentication = original.Reservation.VersionResolution.Coordination.Authentication
			clone = any(copied).(T)
		}
	}
	if original, ok := any(value).(StateV3Authentication); ok {
		if copied, ok := any(clone).(StateV3Authentication); ok {
			copied.Coordination = original.Coordination
			clone = any(copied).(T)
		}
	}
	return clone
}

func validateV3Fixture(t *testing.T, record StateV3Record) error {
	t.Helper()
	policy := fixtureStateV3Policy()
	raw, err := canonicalJSON(record)
	if err != nil {
		t.Fatal(err)
	}
	return ValidateStateV3Record(raw, record, policy, fixtureStateV3Authentication(t, policy, record))
}

func rechainStateV3Events(t *testing.T, events []StateV3Event) []StateV3Event {
	t.Helper()
	result := make([]StateV3Event, 0, len(events))
	for _, event := range events {
		event.Sequence, event.PreviousDigest, event.Digest = 0, "", ""
		var err error
		result, err = appendStateV3Event(result, event)
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func rebindStateV3Events(t *testing.T, record *StateV3Record) {
	t.Helper()
	if record.OutcomeEvidence != nil {
		record.OutcomeEvidence.PriorStateSHA256, _ = stateV3OutcomePriorStateDigest(*record)
	}
	original := append([]StateV3Event(nil), record.Events...)
	record.Events = original
	result := make([]StateV3Event, 0, len(original))
	for index, event := range original {
		event.Sequence, event.PreviousDigest, event.Digest = 0, "", ""
		event.EvidenceSHA256, _ = stateV3ExpectedEventEvidenceDigest(*record, event, index)
		var err error
		result, err = appendStateV3Event(result, event)
		if err != nil {
			t.Fatal(err)
		}
	}
	record.Events = result
}

func removeStateV3MutationArm(t *testing.T, record *StateV3Record, operation string, occurrence int) {
	t.Helper()
	armIndex, matched := 0, 0
	for eventIndex := 0; eventIndex < len(record.Events); eventIndex++ {
		if record.Events[eventIndex].Kind != StateV3EventMutationArmed {
			continue
		}
		if armIndex >= len(record.MutationArms) {
			t.Fatal("arm fixture mismatch")
		}
		if record.MutationArms[armIndex].OperationKind == operation {
			if matched == occurrence {
				record.Events = append(record.Events[:eventIndex], record.Events[eventIndex+1:]...)
				record.MutationArms = append(record.MutationArms[:armIndex], record.MutationArms[armIndex+1:]...)
				if len(record.MutationArms) == 0 {
					record.MutationArms = nil
				}
				return
			}
			matched++
		}
		armIndex++
	}
	t.Fatalf("missing %s arm occurrence %d", operation, occurrence)
}

func fixtureStateV3ArmedSourceBranch(t *testing.T) StateV3Record {
	t.Helper()
	full := fixtureStateV3Record(t)
	record := fixtureStateV3RecordAtEventCount(t, full, 2)
	policy := fixtureStateV3Policy()
	record.Reservation.Command = "release:prepare"
	record.Reservation.RequestedVersion = "v2.11.0"
	record.Reservation.ResolvedVersion = "v2.11.0"
	record.Reservation.DevelopmentVersion = "v2.12.0-dev"
	record.Reservation.GenerationVersion = "v2.12.0-dev"
	record.Reservation.BodySnapshot = "/gardener release:prepare v2.11.0"
	record.Reservation.Marker = Marker("123", "789", record.Reservation.Command, record.Reservation.RequestedVersion)
	context := Context{RepositoryID: "123", RepositoryFullName: RepositoryFullName, IssueNumber: "456", OriginalCommentID: "789", AcknowledgementCommentID: "790", BodySnapshot: record.Reservation.BodySnapshot, PolicyRevision: record.Reservation.PolicyRevision}
	record.Reservation.RequestSHA256 = RequestSHA256(context, record.Reservation.Command, record.Reservation.RequestedVersion)
	record.Reservation.SourceRef = "refs/heads/main"
	record.Binding.RequestSHA256 = record.Reservation.RequestSHA256
	fixtureStateV3VersionResolution(t, &record.Reservation, "v2.11.0-dev")
	record.Prepared.Mutation.TargetRef = "refs/heads/dev-v2.12.x"
	record.Prepared.Mutation.Message = "release: v2.12.0-dev"
	record.Prepared.Mutation.Branches = []StateV3BranchMutationIntent{{Ref: "refs/heads/release-v2.11.x", Target: "source"}, {Ref: "refs/heads/dev-v2.12.x", Target: "source"}, {Ref: "refs/heads/dev-v2.12.x", ExpectedOldOID: record.Reservation.SourceOID, Target: "platform_commit"}}
	record.TagPlans[0].Name = record.Reservation.GenerationVersion
	record.TagPlans[0].Ref = "refs/tags/" + record.Reservation.GenerationVersion
	record.TagPlans[0].Message = record.Reservation.GenerationVersion + "\n"
	record.TagPlans[0].TargetRef = record.Prepared.Mutation.TargetRef
	finalizeStateV3PreparedFixture(t, record.Prepared, record.Reservation, record.TagPlans)
	record.Events = nil
	record.MutationArms = nil
	appendEvent := func(event StateV3Event) {
		event.EvidenceSHA256, _ = stateV3ExpectedEventEvidenceDigest(record, event, len(record.Events))
		var err error
		record.Events, err = appendStateV3Event(record.Events, event)
		if err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(StateV3Event{Kind: StateV3EventReserved, Phase: StateV3PhaseReserved})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhasePrepared})
	intent := record.Prepared.Mutation.Branches[0]
	arm, ok := stateV3MutationArm("branch_ref_create", intent.Ref, intent.ExpectedOldOID, record.Reservation.SourceOID, true, intent)
	if !ok {
		t.Fatal("could not arm source branch")
	}
	record.MutationArms = append(record.MutationArms, arm)
	appendEvent(StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhasePrepared, Ref: arm.Ref, ObjectOID: arm.IntendedObjectOID, Disposition: "armed"})
	if validateStateV3Policy(policy) != nil {
		t.Fatal("invalid fixture policy")
	}
	return record
}
