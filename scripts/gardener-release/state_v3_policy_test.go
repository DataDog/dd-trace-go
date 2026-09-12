// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestStateV3PolicyStrictlyDecodesExplicitSyntheticTrustRoot(t *testing.T) {
	policy := fixtureStateV3Policy()
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeStateV3Policy(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, policy) {
		t.Fatalf("decoded policy differs: %#v", decoded)
	}
	extraRulePolicy := cloneStateV3(t, policy)
	extraRulePolicy.Rulesets.TagImmutability.Rules = append(extraRulePolicy.Rulesets.TagImmutability.Rules, StateV3RulesetRule{Type: "reviewed_additional_control", ParametersSHA256: v3SHAa})
	sort.Slice(extraRulePolicy.Rulesets.TagImmutability.Rules, func(i, j int) bool {
		return stateV3RulesetRuleKey(extraRulePolicy.Rulesets.TagImmutability.Rules[i]) < stateV3RulesetRuleKey(extraRulePolicy.Rulesets.TagImmutability.Rules[j])
	})
	refreshStateV3RulesetAttestation(&extraRulePolicy.Rulesets.TagImmutability)
	if _, err := DecodeStateV3Policy(mustJSON(t, extraRulePolicy)); err != nil {
		t.Fatalf("additional normalized reviewed rule rejected: %v", err)
	}

	for name, mutate := range map[string]func(StateV3Policy) StateV3Policy{
		"production repository is fixed": func(p StateV3Policy) StateV3Policy { p.RepositoryFullName = "attacker/example"; return p },
		"app id required":                func(p StateV3Policy) StateV3Policy { p.App.AppID = ""; return p },
		"installation required":          func(p StateV3Policy) StateV3Policy { p.App.InstallationID = ""; return p },
		"role identity required":         func(p StateV3Policy) StateV3Policy { p.CommitRoles.SignatureSignerGraphQL.Login = ""; return p },
		"minor checkpoint required":      func(p StateV3Policy) StateV3Policy { p.StateLanes.Minor.CheckpointOID = ""; return p },
		"patch history bound required":   func(p StateV3Policy) StateV3Policy { p.StateLanes.Patch.MaxHistoryCommits = 0; return p },
		"minor lane ref is fixed":        func(p StateV3Policy) StateV3Policy { p.StateLanes.Minor.StateRef = "refs/heads/caller"; return p },
		"patch lane ref is fixed":        func(p StateV3Policy) StateV3Policy { p.StateLanes.Patch.StateRef = StateV3MinorStateRef; return p },
		"lane checkpoints are distinct": func(p StateV3Policy) StateV3Policy {
			p.StateLanes.Patch.CheckpointOID = p.StateLanes.Minor.CheckpointOID
			return p
		},
		"policy revision required": func(p StateV3Policy) StateV3Policy { p.PolicyRevision = ""; return p },
		"duplicate state ruleset same revision": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.StateUpdateAuthorization.ID = p.Rulesets.StateHistoryProtection.ID
			p.Rulesets.StateUpdateAuthorization.Revision = p.Rulesets.StateHistoryProtection.Revision
			p.Rulesets.StateUpdateAuthorization.Attestation.RulesetID = p.Rulesets.StateHistoryProtection.ID
			p.Rulesets.StateUpdateAuthorization.Attestation.RulesetRevision = p.Rulesets.StateHistoryProtection.Revision
			return p
		},
		"duplicate tag ruleset same revision": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.TagImmutability.ID = p.Rulesets.TagCreationAuthorization.ID
			p.Rulesets.TagImmutability.Revision = p.Rulesets.TagCreationAuthorization.Revision
			p.Rulesets.TagImmutability.Attestation.RulesetID = p.Rulesets.TagCreationAuthorization.ID
			p.Rulesets.TagImmutability.Attestation.RulesetRevision = p.Rulesets.TagCreationAuthorization.Revision
			return p
		},
		"duplicate branch ruleset": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.BranchUpdateAuthorization = p.Rulesets.BranchCreationAuthorization
			return p
		},
		"ruleset revision exact":    func(p StateV3Policy) StateV3Policy { p.Rulesets.TagImmutability.Revision = "latest"; return p },
		"ruleset enforcement exact": func(p StateV3Policy) StateV3Policy { p.Rulesets.TagImmutability.Enforcement = "evaluate"; return p },
		"missing include patterns": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.TagImmutability.IncludePatterns = []string{}
			return p
		},
		"wrong include patterns": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.TagImmutability.IncludePatterns = []string{"refs/tags/*"}
			return p
		},
		"state rules omit patch lane": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.StateHistoryProtection.IncludePatterns = []string{StateV3MinorStateRef}
			refreshStateV3RulesetAttestation(&p.Rulesets.StateHistoryProtection)
			return p
		},
		"omitted exclusions": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.TagImmutability.ExcludePatterns = nil
			return p
		},
		"extra exclusions": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.TagImmutability.ExcludePatterns = []string{"refs/tags/synthetic-release-namespace/excluded"}
			return p
		},
		"attested asymmetric exclusions": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.TagImmutability.ExcludePatterns = []string{"refs/tags/synthetic-release-namespace/excluded"}
			refreshStateV3RulesetAttestation(&p.Rulesets.TagImmutability)
			return p
		},
		"attested exact state ref exclusion": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.StateHistoryProtection.ExcludePatterns = []string{StateV3MinorStateRef}
			p.Rulesets.StateUpdateAuthorization.ExcludePatterns = []string{StateV3MinorStateRef}
			refreshStateV3RulesetAttestation(&p.Rulesets.StateHistoryProtection)
			refreshStateV3RulesetAttestation(&p.Rulesets.StateUpdateAuthorization)
			return p
		},
		"wrong expected rules": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.TagImmutability.Rules = []StateV3RulesetRule{{Type: "deletion"}}
			return p
		},
		"state required signatures missing": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.StateHistoryProtection.Rules = []StateV3RulesetRule{{Type: "deletion"}, {Type: "non_fast_forward"}}
			refreshStateV3RulesetAttestation(&p.Rulesets.StateHistoryProtection)
			return p
		},
		"semantic namespace not reviewed": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.TagImmutability.Attestation.SemanticNamespaceValidated = false
			return p
		},
		"hidden bypass": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.TagImmutability.Attestation.BypassVisibility = "partial"
			return p
		},
		"admin bypass": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.TagImmutability.Attestation.AdministratorBypassDisabled = false
			return p
		},
		"immutability bypass actor": func(p StateV3Policy) StateV3Policy {
			p.Rulesets.TagImmutability.Attestation.BypassActors = []StateV3RulesetBypassActor{{ActorID: p.App.AppID, ActorType: "Integration", Mode: "always"}}
			return p
		},
		"test policy drift":        func(p StateV3Policy) StateV3Policy { p.Test.WorkflowPath = ".github/workflows/caller.yml"; return p },
		"image policy drift":       func(p StateV3Policy) StateV3Policy { p.Image.WorkflowSHA256 = ""; return p },
		"feedback policy drift":    func(p StateV3Policy) StateV3Policy { p.Feedback.GardenerAuthorID = ""; return p },
		"identity name delimiter":  func(p StateV3Policy) StateV3Policy { p.Tagger.Name = "Release <spoof>"; return p },
		"identity email delimiter": func(p StateV3Policy) StateV3Policy { p.Tagger.Email = "release<spoof>@example.invalid"; return p },
		"identity control":         func(p StateV3Policy) StateV3Policy { p.Tagger.Name = "Release\tApp"; return p },
		"ambiguous email":          func(p StateV3Policy) StateV3Policy { p.Tagger.Email = "a@@example.invalid"; return p },
		"placeholder rejected":     func(p StateV3Policy) StateV3Policy { p.App.Slug = "replace-me"; return p },
	} {
		t.Run(name, func(t *testing.T) {
			candidate, err := json.Marshal(mutate(policy))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeStateV3Policy(candidate); err == nil {
				t.Fatal("invalid v3 policy accepted")
			}
		})
	}
}

func TestStateV3DecodersRejectAmbiguousDocumentsAndSchemaV2(t *testing.T) {
	record := fixtureStateV3Record(t)
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeStateV3Record(raw); err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["schema_version"] = StateSchemaVersion
	v2, _ := json.Marshal(object)
	if _, err := DecodeStateV3Record(v2); ErrorCode(err) != "unsupported_state_v3_version" {
		t.Fatalf("schema v2 error = %q", ErrorCode(err))
	}

	duplicate := strings.Replace(string(raw), `"schema_version":"3"`, `"schema_version":"3","schema_version":"3"`, 1)
	unknown := strings.Replace(string(raw), `"schema_version":"3"`, `"schema_version":"3","unknown":true`, 1)
	for name, candidate := range map[string][]byte{
		"duplicate": []byte(duplicate), "unknown": []byte(unknown), "trailing": append(append([]byte(nil), raw...), []byte(` {}`)...),
		"null": []byte("null"), "nested null": []byte(strings.Replace(string(raw), `"tag_evidence":[{`, `"tag_evidence":[null,{`, 1)),
		"malformed": []byte(`{"schema_version":`), "oversized": make([]byte, MaxStateV3DocumentBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeStateV3Record(candidate); err == nil {
				t.Fatal("ambiguous v3 record accepted")
			}
		})
	}

	policyRaw, _ := json.Marshal(fixtureStateV3Policy())
	policyDuplicate := strings.Replace(string(policyRaw), `"schema_version":"3"`, `"schema_version":"3","schema_version":"3"`, 1)
	policyUnknown := strings.Replace(string(policyRaw), `"schema_version":"3"`, `"schema_version":"3","unknown":true`, 1)
	for name, candidate := range map[string][]byte{
		"duplicate": []byte(policyDuplicate), "unknown": []byte(policyUnknown), "trailing": append(append([]byte(nil), policyRaw...), []byte(` {}`)...),
		"null": []byte("null"), "nested null": []byte(strings.Replace(string(policyRaw), `"slug":"synthetic-release-app"`, `"slug":null`, 1)),
		"malformed": []byte(`{"schema_version":`), "oversized": make([]byte, MaxPolicyBytes+1),
	} {
		t.Run("policy_"+name, func(t *testing.T) {
			if _, err := DecodeStateV3Policy(candidate); err == nil {
				t.Fatal("ambiguous v3 policy accepted")
			}
		})
	}
}

func TestStateV3AppendRecordEventBindsCanonicalTypedEvidence(t *testing.T) {
	record := fixtureStateV3Record(t)
	record.Phase = StateV3PhaseReserved
	record.Prepared, record.Commit = nil, nil
	record.MutationArms, record.BranchEvidence = nil, nil
	record.TagPlans, record.TagIntents, record.TagObjectEvidence, record.TagEvidence = nil, nil, nil, nil
	record.TestEvidence, record.PreparePRIntent, record.PreparePREvidence, record.OutcomeEvidence = nil, nil, nil, nil
	record.Events = nil
	updated, err := AppendStateV3RecordEvent(record, StateV3Event{Kind: StateV3EventReserved, Phase: StateV3PhaseReserved, EvidenceSHA256: v3SHAa})
	if err != nil {
		t.Fatal(err)
	}
	expected, ok := stateV3ExpectedEventEvidenceDigest(updated, updated.Events[0], 0)
	if !ok || updated.Events[0].EvidenceSHA256 != expected {
		t.Fatal("event was not bound to canonical reservation evidence")
	}
	if err := validateV3Fixture(t, updated); err != nil {
		t.Fatalf("typed reserved event rejected: %v", err)
	}
}

func TestStateV3GitOIDsAreExactlyLowercaseSHA1(t *testing.T) {
	for _, value := range []string{"", v3SHAa, strings.ToUpper(v3OIDa), strings.Repeat("g", 40), v3OIDa[:39]} {
		if validStateV3OID(value) {
			t.Fatalf("invalid v3 Git OID accepted: %q", value)
		}
	}
	if !validStateV3OID(v3OIDa) || !lowerHexDigest(v3SHAa) {
		t.Fatal("exact SHA-1 Git OID or SHA-256 digest rejected")
	}
	record := fixtureStateV3Record(t)
	record.Reservation.SourceOID = v3SHAa
	if err := validateV3Fixture(t, record); err == nil {
		t.Fatal("64-character Git OID accepted in v3 record")
	}
}
