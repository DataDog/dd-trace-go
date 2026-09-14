// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"testing"
)

func TestStateV3AcceptsOnlyTheApprovedForwardPhasePrefixes(t *testing.T) {
	full := fixtureStateV3Record(t)
	for _, test := range []struct {
		name     string
		phase    StateV3Phase
		events   int
		prepared bool
		commit   bool
		evidence bool
	}{
		{"reserved", StateV3PhaseReserved, 1, false, false, false},
		{"prepared", StateV3PhasePrepared, 2, true, false, false},
		{"branches", StateV3PhaseBranchesPublished, 6, true, true, false},
		{"tests", StateV3PhaseTestsPassed, 8, true, true, false},
		{"tags", StateV3PhaseTagsPublished, 13, true, true, true},
		{"complete", StateV3PhaseComplete, 15, true, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := fixtureStateV3RecordAtEventCount(t, full, test.events)
			if err := validateV3Fixture(t, record); err != nil {
				t.Fatalf("valid prefix rejected: %v", err)
			}
		})
	}
}

func TestStateV3PreparedPhasePersistsReconciliationBeforeAdvancing(t *testing.T) {
	full := fixtureStateV3Record(t)
	for name, eventCount := range map[string]int{"commit adopted": 4, "branch published": 5} {
		t.Run(name, func(t *testing.T) {
			record := fixtureStateV3RecordAtEventCount(t, full, eventCount)
			if err := validateV3Fixture(t, record); err != nil {
				t.Fatalf("durable prepared reconciliation rejected: %v", err)
			}
		})
	}
}

func TestStateV3TestsPassedPhasePersistsTagReconciliationBeforeAdvancing(t *testing.T) {
	full := fixtureStateV3Record(t)
	record := fixtureStateV3RecordAtEventCount(t, full, 12)
	if err := validateV3Fixture(t, record); err != nil {
		t.Fatalf("durable tag reconciliation rejected: %v", err)
	}
}

func TestStateV3RejectsIncompleteOrUnboundTestAndOutcomeEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*StateV3Record){
		"test release oid":     func(record *StateV3Record) { record.TestEvidence.ReleaseOID = v3OIDd },
		"test workflow":        func(record *StateV3Record) { record.TestEvidence.WorkflowID = "99999" },
		"test run attempt":     func(record *StateV3Record) { record.TestEvidence.Runs[0].Attempt = 2 },
		"test run conclusion":  func(record *StateV3Record) { record.TestEvidence.Runs[0].Conclusion = "failure" },
		"test job conclusion":  func(record *StateV3Record) { record.TestEvidence.Runs[0].Jobs[0].Conclusion = "failure" },
		"test jobs incomplete": func(record *StateV3Record) { record.TestEvidence.Runs[0].Jobs = record.TestEvidence.Runs[0].Jobs[:1] },
		"outcome request":      func(record *StateV3Record) { record.OutcomeEvidence.RequestKey = "123:999" },
		"outcome command":      func(record *StateV3Record) { record.OutcomeEvidence.Command = "release:release" },
		"outcome release oid":  func(record *StateV3Record) { record.OutcomeEvidence.ReleaseOID = v3OIDd },
		"outcome publication":  func(record *StateV3Record) { record.OutcomeEvidence.Publication = WorkFailed },
	} {
		t.Run(name, func(t *testing.T) {
			record := fixtureStateV3Record(t)
			mutate(&record)
			rebindStateV3Events(t, &record)
			if err := validateV3Fixture(t, record); err == nil {
				t.Fatal("invalid typed evidence accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*StateV3Record){
		"test evidence absent":    func(record *StateV3Record) { record.TestEvidence = nil },
		"outcome evidence absent": func(record *StateV3Record) { record.OutcomeEvidence = nil },
		"arbitrary test hash": func(record *StateV3Record) {
			record.Events[5].EvidenceSHA256 = v3SHAa
			record.Events = rechainStateV3Events(t, record.Events)
		},
		"reused outcome hash": func(record *StateV3Record) {
			record.Events[9].EvidenceSHA256 = record.Events[5].EvidenceSHA256
			record.Events = rechainStateV3Events(t, record.Events)
		},
	} {
		t.Run(name, func(t *testing.T) {
			record := fixtureStateV3Record(t)
			mutate(&record)
			if err := validateV3Fixture(t, record); err == nil {
				t.Fatal("missing or arbitrary evidence accepted")
			}
		})
	}
}

func TestStateV3ReleaseOutcomeBindsExactImageAndTagEvidence(t *testing.T) {
	record := fixtureReleaseStateV3Record(t)
	if err := validateV3Fixture(t, record); err != nil {
		t.Fatalf("valid release image outcome rejected: %v", err)
	}
	for name, mutate := range map[string]func(*StateV3Record){
		"image workflow id": func(candidate *StateV3Record) { candidate.OutcomeEvidence.ImageEvidence[0].WorkflowID = "99999" },
		"image workflow": func(candidate *StateV3Record) {
			candidate.OutcomeEvidence.ImageEvidence[0].Detail.WorkflowSHA256 = v3SHAc
		},
		"image run attempt": func(candidate *StateV3Record) { candidate.OutcomeEvidence.ImageEvidence[0].Detail.RunAttempt = 2 },
		"image conclusion": func(candidate *StateV3Record) {
			candidate.OutcomeEvidence.ImageEvidence[0].Detail.BuildConclusion = "failure"
		},
		"image commit": func(candidate *StateV3Record) { candidate.OutcomeEvidence.ImageEvidence[0].Detail.CommitSHA = v3OIDd },
		"image tag object": func(candidate *StateV3Record) {
			candidate.OutcomeEvidence.ImageEvidence[0].Detail.ModuleTagObjectSHA = v3OIDd
		},
		"root tag object": func(candidate *StateV3Record) {
			candidate.OutcomeEvidence.ImageEvidence[0].Detail.RootTagObjectSHA = v3OIDd
		},
		"image missing": func(candidate *StateV3Record) {
			candidate.OutcomeEvidence.ImageEvidence = candidate.OutcomeEvidence.ImageEvidence[:3]
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneStateV3(t, record)
			mutate(&candidate)
			rebindStateV3Events(t, &candidate)
			if err := validateV3Fixture(t, candidate); err == nil {
				t.Fatal("invalid release image outcome accepted")
			}
		})
	}
}

func TestStateV3RejectsReservationExecutionAndBranchBindingDrift(t *testing.T) {
	base := fixtureStateV3Record(t)
	for name, mutate := range map[string]func(*StateV3Record){
		"request hash":     func(r *StateV3Record) { r.Binding.RequestSHA256 = v3SHAc },
		"policy revision":  func(r *StateV3Record) { r.Reservation.PolicyRevision = v3SHAc; r.Binding.PolicyRevision = v3SHAc },
		"workflow ref":     func(r *StateV3Record) { r.Binding.WorkflowRef = "refs/heads/caller" },
		"workflow path":    func(r *StateV3Record) { r.Binding.WorkflowPath = ".github/workflows/caller.yml" },
		"workflow file":    func(r *StateV3Record) { r.Binding.WorkflowFileSHA256 = "" },
		"workflow attempt": func(r *StateV3Record) { r.Binding.WorkflowRunAttempt = 0 },
		"line mismatch":    func(r *StateV3Record) { r.Reservation.GenerationVersion = "v2.12.0-rc.1" },
		"command mismatch": func(r *StateV3Record) { r.Reservation.Command = "release:release" },
		"substituted base patch and lane": func(r *StateV3Record) {
			r.Reservation.ResolvedVersion = "v2.11.1-rc.99"
			r.Reservation.GenerationVersion = r.Reservation.ResolvedVersion
			r.Reservation.VersionResolution.Result.ResolvedVersion = r.Reservation.ResolvedVersion
		},
		"substituted RC number": func(r *StateV3Record) {
			r.Reservation.ResolvedVersion = "v2.11.0-rc.99"
			r.Reservation.GenerationVersion = r.Reservation.ResolvedVersion
			r.Reservation.VersionResolution.Result.ResolvedVersion = r.Reservation.ResolvedVersion
		},
		"source version disagreement": func(r *StateV3Record) { r.Reservation.VersionResolution.Source.Version = "v2.11.1-dev" },
		"source branch observation missing": func(r *StateV3Record) {
			r.Reservation.VersionResolution.RemoteRefs.Branches = []StateV3RefObservation{}
		},
		"higher RC observation ignored": func(r *StateV3Record) {
			ref := "refs/tags/v2.11.0-rc.2"
			r.Reservation.VersionResolution.RemoteRefs.Tags = []StateV3RefObservation{{Ref: ref, OID: v3OIDd}}
			r.Reservation.VersionResolution.RemoteRefs.IncompleteDerivations = []StateV3IncompleteTagDerivation{fixtureStateV3IncompleteTagDerivation(t, r.Reservation, "v2.11.0-rc.2", v3OIDd, []string{ref})}
		},
		"missing branch": func(r *StateV3Record) { r.Prepared.Mutation.Branches = nil },
		"branch old oid": func(r *StateV3Record) { r.Prepared.Mutation.Branches[0].ExpectedOldOID = v3OIDb },
		"branch target":  func(r *StateV3Record) { r.Prepared.Mutation.Branches[0].Target = "source" },
	} {
		t.Run(name, func(t *testing.T) {
			record := cloneStateV3(t, base)
			mutate(&record)
			if err := validateV3Fixture(t, record); err == nil {
				t.Fatal("drifted v3 binding accepted")
			}
		})
	}
}

func TestStateV3VersionResolutionEvidenceRecomputesAuthoritativeInputs(t *testing.T) {
	record := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	resolution := &record.Reservation.VersionResolution
	resolution.RemoteRefs.Tags = []StateV3RefObservation{{Ref: "refs/tags/v2.11.1", OID: v3OIDd}}
	resolution.RemoteRefs.IncompleteTagVersions = []string{"v2.11.1"}
	resolution.RemoteRefs.IncompleteDerivations = []StateV3IncompleteTagDerivation{fixtureStateV3IncompleteTagDerivation(t, record.Reservation, "v2.11.1", v3OIDd, []string{"refs/tags/v2.11.1"})}
	fixtureStateV3CoordinationClaim(t, &record.Reservation)
	rebindStateV3Events(t, &record)
	if err := validateV3Fixture(t, record); err == nil {
		t.Fatal("self-attested historical tagger plan authorized version resolution before strict backend execution")
	}
	for name, mutate := range map[string]func(*StateV3Record){
		"invalid observed oid": func(candidate *StateV3Record) { candidate.Reservation.VersionResolution.RemoteRefs.Tags[0].OID = "sha" },
		"missing derivation": func(candidate *StateV3Record) {
			candidate.Reservation.VersionResolution.RemoteRefs.IncompleteDerivations = nil
		},
		"wrong present derivation": func(candidate *StateV3Record) {
			candidate.Reservation.VersionResolution.RemoteRefs.IncompleteDerivations[0].PresentTagRefs = []string{}
		},
		"source raw disagreement": func(candidate *StateV3Record) {
			candidate.Reservation.VersionResolution.Source.Raw = []byte("package version\n\nvar Tag = \"v2.11.1-dev\"\n")
		},
		"remote observations incomplete": func(candidate *StateV3Record) { candidate.Reservation.VersionResolution.RemoteRefs.Complete = false },
		"unsorted branch observations": func(candidate *StateV3Record) {
			candidate.Reservation.VersionResolution.RemoteRefs.Branches = []StateV3RefObservation{{Ref: "refs/heads/z", OID: v3OIDb}, {Ref: candidate.Reservation.SourceRef, OID: candidate.Reservation.SourceOID}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneStateV3(t, record)
			mutate(&candidate)
			rebindStateV3Events(t, &candidate)
			if err := validateV3Fixture(t, candidate); err == nil {
				t.Fatal("invalid version-resolution evidence accepted")
			}
		})
	}
}

func TestStateV3RejectsCapacityPathProvenanceAndMutationDrift(t *testing.T) {
	base := fixtureStateV3Record(t)
	for name, mutate := range map[string]func(*StateV3Record){
		"bundle cap plus one": func(r *StateV3Record) {
			r.Prepared.Bundle.SizeBytes = MaxStateV3PreparedBundleBytes + 1
			r.Prepared.StateFiles[0].SizeBytes++
			r.Prepared.TotalDecodedAdditionBytes++
		},
		"decoded cap plus one":         func(r *StateV3Record) { r.Prepared.TotalDecodedAdditionBytes = MaxStateV3DecodedAdditionBytes + 1 },
		"addition count":               func(r *StateV3Record) { r.Prepared.AdditionCount = 4 },
		"prepared path":                func(r *StateV3Record) { r.Prepared.StateFiles[1].Path = "caller/prepared.json" },
		"bundle path":                  func(r *StateV3Record) { r.Prepared.Bundle.Path = "caller/generation.bundle" },
		"bundle prerequisite":          func(r *StateV3Record) { r.Prepared.Bundle.PrerequisiteOID = v3OIDb },
		"missing tool provenance":      func(r *StateV3Record) { r.Prepared.ToolDigest = "" },
		"missing validator provenance": func(r *StateV3Record) { r.Prepared.ValidatorDigest = "" },
		"caller repository":            func(r *StateV3Record) { r.Prepared.Mutation.RepositoryFullName = "attacker/example" },
		"caller ref":                   func(r *StateV3Record) { r.Prepared.Mutation.TargetRef = "refs/heads/caller" },
		"expected head":                func(r *StateV3Record) { r.Prepared.Mutation.ExpectedHeadOID = v3OIDb },
		"expected tree":                func(r *StateV3Record) { r.Prepared.Mutation.ExpectedTreeOID = r.Reservation.SourceOID },
		"message":                      func(r *StateV3Record) { r.Prepared.Mutation.Message = "caller message" },
		"change path":                  func(r *StateV3Record) { r.Prepared.Mutation.FileChanges[0].Path = "../escape" },
		"change mode":                  func(r *StateV3Record) { r.Prepared.Mutation.FileChanges[0].Mode = "100755" },
		"change identity":              func(r *StateV3Record) { r.Prepared.Mutation.FileChanges[0].SHA256 = v3SHAc },
	} {
		t.Run(name, func(t *testing.T) {
			record := cloneStateV3(t, base)
			mutate(&record)
			if err := validateV3Fixture(t, record); err == nil {
				t.Fatal("invalid v3 prepared state accepted")
			}
		})
	}
}
