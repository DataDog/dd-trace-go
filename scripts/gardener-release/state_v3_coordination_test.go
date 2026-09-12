// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"testing"
)

func TestStateV3CoordinationClaimRejectsStaleAndSameLineClaims(t *testing.T) {
	minor := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	patchDifferent := fixtureStateV3PatchOperation(t, "v2.10", "v2.10.1")
	fixtureStateV3SetCoordinationClaims(t, &minor.Reservation.VersionResolution, []StateV3ReleaseLineClaimEvidence{patchDifferent.Reservation.CoordinationClaim})
	minor.Reservation.VersionResolution.ExistingOperations = []ExistingOperation{{
		RequestKey: patchDifferent.Reservation.RequestKey, RequestSHA256: patchDifferent.Reservation.RequestSHA256,
		Command: patchDifferent.Reservation.Command, ReleaseLine: patchDifferent.Reservation.ReleaseLine,
		ResolvedVersion: patchDifferent.Reservation.ResolvedVersion, DevelopmentVersion: patchDifferent.Reservation.DevelopmentVersion,
		Phase: string(patchDifferent.Phase),
	}}
	fixtureStateV3CoordinationClaim(t, &minor.Reservation)
	rebindStateV3Events(t, &minor)
	if err := validateV3Fixture(t, minor); err != nil {
		t.Fatalf("different release-line claims rejected: %v", err)
	}

	stale := cloneStateV3(t, minor)
	stale.Reservation.CoordinationClaim.Commit.ParentOID = v3OIDd
	rebindStateV3Events(t, &stale)
	if err := validateV3Fixture(t, stale); err == nil {
		t.Fatal("claim with stale coordination parent accepted")
	}

	sameLine := fixtureStateV3PatchOperation(t, "v2.11", "v2.11.1")
	blocked := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	fixtureStateV3SetCoordinationClaims(t, &blocked.Reservation.VersionResolution, []StateV3ReleaseLineClaimEvidence{sameLine.Reservation.CoordinationClaim})
	blocked.Reservation.VersionResolution.ExistingOperations = []ExistingOperation{{
		RequestKey: sameLine.Reservation.RequestKey, RequestSHA256: sameLine.Reservation.RequestSHA256,
		Command: sameLine.Reservation.Command, ReleaseLine: sameLine.Reservation.ReleaseLine,
		ResolvedVersion: sameLine.Reservation.ResolvedVersion, DevelopmentVersion: sameLine.Reservation.DevelopmentVersion,
		Phase: string(sameLine.Phase),
	}}
	fixtureStateV3CoordinationClaim(t, &blocked.Reservation)
	rebindStateV3Events(t, &blocked)
	if err := validateV3Fixture(t, blocked); err == nil {
		t.Fatal("same-line coordination claim accepted")
	}
}

func TestStateV3HistoricalTagDerivationUsesHistoricalSource(t *testing.T) {
	record := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	version := "v2.11.1"
	historicalOID := "1212121212121212121212121212121212121212"
	historicalTreeOID := "1313131313131313131313131313131313131313"
	manifestOID := "1414141414141414141414141414141414141414"
	plan := map[string]any{
		"schema_version": "1", "source_sha": historicalOID, "branch": "release-v2.11.x", "requested_version": version,
		"root_module":            "github.com/DataDog/dd-trace-go/v2",
		"modules":                []any{map[string]any{"path": "github.com/DataDog/dd-trace-go/v2", "dir": ".", "tagged": true}, map[string]any{"path": "github.com/DataDog/dd-trace-go/contrib/a", "dir": "contrib/a", "tagged": true}},
		"permitted_output_files": []any{"internal/version/version.go"}, "expected_tags": []any{version, "contrib/a/" + version},
	}
	raw, err := canonicalJSON(plan)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	policy := fixtureStateV3Policy()
	derivation := StateV3IncompleteTagDerivation{
		Version: version, ManifestOID: manifestOID, PeeledCommitOID: historicalOID,
		RootTag:          StateV3HistoricalTagEvidence{TagObjectOID: manifestOID, ObjectType: "tag", TargetType: "commit", PeeledCommitOID: historicalOID, Signature: "absent"},
		SourceTreeOID:    historicalTreeOID,
		HistoricalCommit: StateV3StateCommitEvidence{OID: historicalOID, TreeOID: historicalTreeOID, ChangedPaths: []StateV3ChangedPath{}, RESTVerified: true, RESTReason: StateV3RequiredRESTVerificationReason, GraphQLSignatureValid: true, WasSignedByGitHub: true, SignatureState: StateV3RequiredSignatureState, Roles: policy.CommitRoles},
		HistoricalTree:   StateV3StateTreeEvidence{OID: historicalTreeOID, Complete: true, Entries: []StateV3StateTreeEntry{{Path: "go.mod", Mode: "100644", Type: "blob", OID: v3OIDa}}},
		ToolPath:         policy.TaggerArtifact.Path, ToolSHA256: policy.TaggerArtifact.SHA256, ToolArtifact: policy.TaggerArtifact,
		Execution: StateV3PlanExecutionAttestation{SchemaVersion: "1", Collector: "strict_github_backend", ToolArtifact: policy.TaggerArtifact, SourceCommitOID: historicalOID, SourceTreeOID: historicalTreeOID, PlanSHA256: hex.EncodeToString(digest[:]), PlanBlobOID: stateV3GitBlobOID(raw), Verified: true},
		PlanRaw:   raw, PlanSHA256: hex.EncodeToString(digest[:]), PlanBlobOID: stateV3GitBlobOID(raw),
		ExpectedTagRefs: []string{"refs/tags/contrib/a/" + version, "refs/tags/" + version}, PresentTagRefs: []string{"refs/tags/" + version},
	}
	sort.Strings(derivation.ExpectedTagRefs)
	record.Reservation.VersionResolution.RemoteRefs.Tags = []StateV3RefObservation{{Ref: "refs/tags/" + version, OID: manifestOID}}
	record.Reservation.VersionResolution.RemoteRefs.IncompleteTagVersions = []string{version}
	record.Reservation.VersionResolution.RemoteRefs.IncompleteDerivations = []StateV3IncompleteTagDerivation{derivation}
	fixtureStateV3CoordinationClaim(t, &record.Reservation)
	rebindStateV3Events(t, &record)
	if err := validateV3Fixture(t, record); err == nil {
		t.Fatal("self-attested historical tagger plan authorized resolution before strict backend execution")
	}

	forged := cloneStateV3(t, record)
	forged.Reservation.VersionResolution.RemoteRefs.IncompleteDerivations[0].ToolSHA256 = v3SHAb
	rebindStateV3Events(t, &forged)
	if err := validateV3Fixture(t, forged); err == nil {
		t.Fatal("unapproved historical tagger artifact accepted")
	}
}
