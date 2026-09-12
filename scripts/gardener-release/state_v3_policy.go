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
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
)

func DecodeStateV3Policy(raw []byte) (StateV3Policy, error) {
	var policy StateV3Policy
	if err := decodeStateV3Document(raw, MaxPolicyBytes, &policy); err != nil {
		return StateV3Policy{}, newReleaseError(ErrorClassContractMismatch, "invalid_state_v3_policy")
	}
	if err := validateStateV3Policy(policy); err != nil {
		return StateV3Policy{}, err
	}
	return policy, nil
}

func DecodeStateV3Record(raw []byte) (StateV3Record, error) {
	var record StateV3Record
	if err := decodeStateV3Document(raw, MaxStateV3DocumentBytes, &record); err != nil {
		return StateV3Record{}, newReleaseError(ErrorClassStateConflict, "invalid_state_v3_record")
	}
	if record.SchemaVersion != StateV3SchemaVersion {
		return StateV3Record{}, newReleaseError(ErrorClassStateConflict, "unsupported_state_v3_version")
	}
	return record, nil
}

func decodeStateV3Document(raw []byte, maxBytes int, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(raw) > maxBytes || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("invalid_state_v3_json")
	}
	if err := validateJSONNoDuplicateKeys(raw, maxBytes); err != nil {
		return err
	}
	if err := validateStateV3NoNull(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing_state_v3_json")
	}
	return nil
}

func validateStateV3NoNull(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if token == nil {
			return errors.New("null_state_v3_json")
		}
	}
}

func validStateV3CoordinationPolicy(policy StateV3CoordinationPolicy) bool {
	// Coordination retains only compact per-line claim lifecycle commits. The
	// reviewed checkpoint rotation procedure must occur with no active claims.
	return policy.StateRef == StateV3CoordinationRef && validStateV3OID(policy.CheckpointOID) && policy.MaxHistoryCommits >= 2 && policy.MaxHistoryCommits <= MaxStateV3HistoryCommits
}

func validStateV3TrustedArtifact(artifact StateV3TrustedArtifact) bool {
	if artifact.Path != "scripts/autoreleasetagger" || len(artifact.Raw) == 0 || !lowerHexDigest(artifact.SHA256) || !validStateV3OID(artifact.BlobOID) || artifact.TreeOID != artifact.Tree.OID || !validStateV3CompleteTree(artifact.Tree) {
		return false
	}
	digest := sha256.Sum256(artifact.Raw)
	if artifact.SHA256 != hex.EncodeToString(digest[:]) || artifact.BlobOID != stateV3GitBlobOID(artifact.Raw) {
		return false
	}
	for _, entry := range artifact.Tree.Entries {
		if entry.Path == artifact.Path {
			return entry.OID == artifact.BlobOID
		}
	}
	return false
}

func validStateV3LanePolicy(lane StateV3LanePolicy, expectedRef string) bool {
	minimum := 4*MaxStateV3Tags + StateV3OtherHistoryOverhead
	if expectedRef == StateV3MinorStateRef {
		minimum = 4*MaxStateV3Tags + StateV3PrepareHistoryOverhead
	}
	return lane.StateRef == expectedRef && validStateV3OID(lane.CheckpointOID) && lane.MaxHistoryCommits >= minimum && lane.MaxHistoryCommits <= MaxStateV3HistoryCommits
}

func stateV3LaneForReservation(reservation StateV3Reservation, policy StateV3Policy) (StateV3LanePolicy, bool) {
	version, err := ParseReleaseVersion(reservation.ResolvedVersion)
	if err != nil {
		return StateV3LanePolicy{}, false
	}
	if version.Patch == 0 {
		return policy.StateLanes.Minor, true
	}
	return policy.StateLanes.Patch, true
}

func validateStateV3Policy(policy StateV3Policy) error {
	invalid := func() error { return newReleaseError(ErrorClassContractMismatch, "invalid_state_v3_policy") }
	if policy.SchemaVersion != StateV3PolicySchemaVersion || !lowerHexDigest(policy.PolicyRevision) || !validID(policy.RepositoryID) || policy.RepositoryFullName != RepositoryFullName || !validStateV3LanePolicy(policy.StateLanes.Minor, StateV3MinorStateRef) || !validStateV3LanePolicy(policy.StateLanes.Patch, StateV3PatchStateRef) || !validStateV3CoordinationPolicy(policy.Coordination) || !validStateV3TrustedArtifact(policy.TaggerArtifact) || policy.StateLanes.Minor.CheckpointOID == policy.StateLanes.Patch.CheckpointOID || policy.StateLanes.Minor.CheckpointOID == policy.Coordination.CheckpointOID || policy.StateLanes.Patch.CheckpointOID == policy.Coordination.CheckpointOID {
		return invalid()
	}
	if !validStateV3App(policy.App) || !validStateV3Roles(policy.CommitRoles) || !validStateV3RawIdentity(policy.Tagger) || policy.CommitRoles.AuthorREST.Login != policy.App.BotLogin || policy.CommitRoles.AuthorREST.DatabaseID != policy.App.BotDatabaseID || policy.CommitRoles.AuthorGraphQL.Login != policy.App.BotLogin || policy.CommitRoles.AuthorGraphQL.DatabaseID != policy.App.BotDatabaseID {
		return invalid()
	}
	limits := policy.Limits
	if limits.PreparedBundleBytes <= 0 || limits.PreparedBundleBytes > MaxStateV3PreparedBundleBytes || limits.DecodedAdditionBytes <= 0 || limits.DecodedAdditionBytes > MaxStateV3DecodedAdditionBytes || limits.ReleaseFileChanges <= 0 || limits.ReleaseFileChanges > MaxStateV3ReleaseFileChanges {
		return invalid()
	}
	type expectedRuleset struct {
		policy    StateV3RulesetPolicy
		namespace string
		rules     []string
		appBypass bool
	}
	expected := []expectedRuleset{
		{policy.Rulesets.StateHistoryProtection, "state", []string{"deletion", "non_fast_forward", "required_signatures"}, false},
		{policy.Rulesets.StateUpdateAuthorization, "state", []string{"update"}, true},
		{policy.Rulesets.CoordinationHistoryProtection, "coordination", []string{"deletion", "non_fast_forward", "required_signatures"}, false},
		{policy.Rulesets.CoordinationUpdateAuthorization, "coordination", []string{"update"}, true},
		{policy.Rulesets.BranchCreationAuthorization, "release_branches", []string{"creation"}, true},
		{policy.Rulesets.BranchUpdateAuthorization, "release_branches", []string{"update"}, true},
		{policy.Rulesets.TagCreationAuthorization, "release_tags", []string{"creation"}, true},
		{policy.Rulesets.TagImmutability, "release_tags", []string{"deletion", "non_fast_forward", "required_signatures", "update"}, false},
	}
	seen := map[string]bool{}
	for _, item := range expected {
		ruleset := item.policy
		if seen[ruleset.ID] || !validStateV3RulesetPolicy(ruleset, item.namespace, item.rules, policy, item.appBypass) {
			return invalid()
		}
		seen[ruleset.ID] = true
	}
	if !sameStateV3RulesetConditions(policy.Rulesets.StateHistoryProtection, policy.Rulesets.StateUpdateAuthorization) || !sameStateV3RulesetConditions(policy.Rulesets.CoordinationHistoryProtection, policy.Rulesets.CoordinationUpdateAuthorization) || !sameStateV3RulesetConditions(policy.Rulesets.BranchCreationAuthorization, policy.Rulesets.BranchUpdateAuthorization) || !sameStateV3RulesetConditions(policy.Rulesets.TagCreationAuthorization, policy.Rulesets.TagImmutability) {
		return invalid()
	}
	if !validStateV3TestPolicy(policy.Test) || !validStateV3ImagePolicy(policy.Image) || !validID(policy.Feedback.GardenerAuthorID) || !validStateV3Token(policy.Feedback.GardenerAuthorLogin) {
		return invalid()
	}
	return nil
}

func validStateV3RulesetPolicy(ruleset StateV3RulesetPolicy, namespace string, expectedRules []string, policy StateV3Policy, appBypass bool) bool {
	if !validID(ruleset.ID) || !validStateV3Revision(ruleset.Revision) || ruleset.Namespace != namespace || ruleset.Enforcement != "active" || len(ruleset.IncludePatterns) == 0 || ruleset.ExcludePatterns == nil || len(ruleset.ExcludePatterns) != 0 || !validStateV3Patterns(ruleset.IncludePatterns, namespace) || len(ruleset.Rules) == 0 || !sort.SliceIsSorted(ruleset.Rules, func(i, j int) bool {
		return stateV3RulesetRuleKey(ruleset.Rules[i]) < stateV3RulesetRuleKey(ruleset.Rules[j])
	}) {
		return false
	}
	if namespace == "state" && !reflect.DeepEqual(ruleset.IncludePatterns, []string{StateV3MinorStateRef, StateV3PatchStateRef}) {
		return false
	}
	if namespace == "coordination" && !reflect.DeepEqual(ruleset.IncludePatterns, []string{StateV3CoordinationRef}) {
		return false
	}
	capabilities := map[string]bool{}
	previous := ""
	for _, rule := range ruleset.Rules {
		key := stateV3RulesetRuleKey(rule)
		if !validStateV3Token(rule.Type) || rule.ParametersSHA256 != "" && !lowerHexDigest(rule.ParametersSHA256) || key == previous || capabilities[rule.Type] {
			return false
		}
		previous = key
		capabilities[rule.Type] = true
	}
	for _, required := range expectedRules {
		if !capabilities[required] {
			return false
		}
	}
	return validStateV3RulesetAttestation(ruleset, policy.App, appBypass)
}

func stateV3RulesetRuleKey(rule StateV3RulesetRule) string {
	return rule.Type + "\x00" + rule.ParametersSHA256
}

func validStateV3Patterns(patterns []string, namespace string) bool {
	prefix := map[string]string{"state": "refs/heads/", "coordination": "refs/heads/", "release_branches": "refs/heads/", "release_tags": "refs/tags/"}[namespace]
	if prefix == "" || !sort.StringsAreSorted(patterns) {
		return false
	}
	seen := map[string]bool{}
	for _, pattern := range patterns {
		if !validStateV3Text(pattern, 256) || !strings.HasPrefix(pattern, prefix) || seen[pattern] {
			return false
		}
		seen[pattern] = true
	}
	return true
}

func sameStateV3RulesetConditions(left, right StateV3RulesetPolicy) bool {
	return left.Namespace == right.Namespace && reflect.DeepEqual(left.IncludePatterns, right.IncludePatterns) && reflect.DeepEqual(left.ExcludePatterns, right.ExcludePatterns)
}

func validStateV3RulesetAttestation(ruleset StateV3RulesetPolicy, app StateV3AppIdentity, appBypass bool) bool {
	attestation := ruleset.Attestation
	conditionsDigest, conditionsOK := stateV3CanonicalDigest(struct {
		Namespace string   `json:"namespace"`
		Includes  []string `json:"includes"`
		Excludes  []string `json:"excludes"`
	}{ruleset.Namespace, ruleset.IncludePatterns, ruleset.ExcludePatterns})
	rulesDigest, rulesOK := stateV3CanonicalDigest(ruleset.Rules)
	if !conditionsOK || !rulesOK || !attestation.SemanticNamespaceValidated || attestation.ConditionsSHA256 != conditionsDigest || attestation.RulesSHA256 != rulesDigest || attestation.SchemaVersion != "1" || attestation.RulesetID != ruleset.ID || attestation.RulesetRevision != ruleset.Revision || attestation.BypassVisibility != "complete" || !attestation.AdministratorBypassDisabled || !validStateV3AssociatedIdentity(attestation.Reviewer) || !validStateV3Revision(attestation.ObservedAt) {
		return false
	}
	if !appBypass {
		return len(attestation.BypassActors) == 0
	}
	return len(attestation.BypassActors) == 1 && attestation.BypassActors[0] == (StateV3RulesetBypassActor{ActorID: app.AppID, ActorType: "Integration", Mode: "always"})
}

func validStateV3TestPolicy(policy StateV3TestPolicy) bool {
	if !validID(policy.WorkflowID) || policy.WorkflowPath != MainBranchTestWorkflowPath || !lowerHexDigest(policy.WorkflowSHA256) || policy.Event != "push" || policy.DeadlineSeconds <= 0 || policy.DeadlineSeconds > MaxPollingDeadlineSeconds || len(policy.RequiredJobs) == 0 || len(policy.RequiredJobs) > 100 || !sort.StringsAreSorted(policy.RequiredJobs) {
		return false
	}
	seen := map[string]bool{}
	for _, job := range policy.RequiredJobs {
		if !validStateV3Text(job, 200) || seen[job] {
			return false
		}
		seen[job] = true
	}
	return true
}

func validStateV3ImagePolicy(policy StateV3ImagePolicy) bool {
	return validID(policy.WorkflowID) && policy.WorkflowPath == ImageWorkflowPath && lowerHexDigest(policy.WorkflowSHA256) && lowerHexDigest(policy.ChildWorkflowSHA256)
}

func validStateV3App(app StateV3AppIdentity) bool {
	return validID(app.AppID) && validID(app.InstallationID) && validID(app.BotDatabaseID) && validStateV3Token(app.Slug) && validStateV3Token(app.BotLogin)
}

func validStateV3RawIdentity(identity StateV3RawIdentity) bool {
	return validStateV3GitIdentityPart(identity.Name, 128) && validStateV3Email(identity.Email)
}

func validStateV3GitIdentityPart(value string, max int) bool {
	if !validStateV3Text(value, max) || strings.ContainsAny(value, "<>") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validStateV3Email(value string) bool {
	if !validStateV3GitIdentityPart(value, 254) || strings.Count(value, "@") != 1 || strings.ContainsAny(value, " ") {
		return false
	}
	parts := strings.Split(value, "@")
	if parts[0] == "" || parts[1] == "" || strings.HasPrefix(parts[0], ".") || strings.HasSuffix(parts[0], ".") || strings.Contains(parts[0], "..") || !strings.Contains(parts[1], ".") {
		return false
	}
	for _, r := range parts[0] {
		if r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(".!#$%&'*+/=?^_`{|}~-[]", r) {
			return false
		}
	}
	for _, label := range strings.Split(parts[1], ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
				return false
			}
		}
	}
	return true
}

func validStateV3AssociatedIdentity(identity StateV3AssociatedIdentity) bool {
	return validStateV3Token(identity.Login) && validID(identity.DatabaseID) && (identity.Type == "Bot" || identity.Type == "User")
}

func validStateV3Roles(roles StateV3CommitRoles) bool {
	if !validStateV3RawIdentity(roles.AuthorRaw) || !validStateV3AssociatedIdentity(roles.AuthorREST) || !validStateV3AssociatedIdentity(roles.AuthorGraphQL) || !sameStateV3AssociatedPrincipal(roles.AuthorREST, roles.AuthorGraphQL) || !validStateV3RawIdentity(roles.CommitterRaw) || !validStateV3OptionalIdentity(roles.CommitterREST) || !validStateV3OptionalIdentity(roles.CommitterGraphQL) || !validStateV3AssociatedIdentity(roles.SignatureSignerGraphQL) {
		return false
	}
	return !roles.CommitterREST.Present || !roles.CommitterGraphQL.Present || sameStateV3AssociatedPrincipal(roles.CommitterREST.Identity, roles.CommitterGraphQL.Identity)
}

func validStateV3OptionalIdentity(observation StateV3OptionalAssociatedIdentity) bool {
	if observation.Present {
		return validStateV3AssociatedIdentity(observation.Identity)
	}
	return observation.Identity == (StateV3AssociatedIdentity{})
}

func sameStateV3AssociatedPrincipal(left, right StateV3AssociatedIdentity) bool {
	return left.Login == right.Login && left.DatabaseID == right.DatabaseID
}

func validStateV3Token(value string) bool {
	return validStateV3Text(value, 128) && !strings.ContainsAny(value, " /\\:@")
}

func validStateV3Text(value string, max int) bool {
	return value != "" && len(value) <= max && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00") && !looksPlaceholder(value)
}

func validStateV3OID(value string) bool {
	if len(value) != 40 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validStateV3Revision(value string) bool {
	if !validStateV3Text(value, 128) {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && parsed.Format(time.RFC3339Nano) == value
}
