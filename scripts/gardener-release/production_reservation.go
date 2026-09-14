// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ProductionReservationInput contains trusted workflow data only.
type ProductionReservationInput struct {
	DispatchJSON []byte
	Policy       Policy
	WorkflowSHA  string
	ToolSHA      string
	ToolPath     string
}

// ProductionReservationResult is the durable state snapshot after reservation.
type ProductionReservationResult struct {
	Record    Record `json:"record"`
	StateHead string `json:"state_head"`
	Resumed   bool   `json:"resumed"`
}

// ReserveProductionOperation revalidates comments, resolves remote evidence,
// and persists one immutable reservation plus signing intent.
func ReserveProductionOperation(ctx context.Context, input ProductionReservationInput) (result ProductionReservationResult, err error) {
	if !ValidGitObjectID(input.WorkflowSHA) || !ValidGitObjectID(input.ToolSHA) || input.ToolPath == "" {
		return result, newReleaseError(ErrorClassContractMismatch, "invalid_reservation_runtime")
	}
	token, ok := os.LookupEnv(PublicationTokenEnvironment)
	if !ok || token == "" {
		return result, newReleaseError(ErrorClassEvidenceIncomplete, "publication_token_unavailable")
	}
	api := NewProductionGitHubClient(token)
	validated, err := ValidateDispatchFromGitHub(ctx, api, input.DispatchJSON, input.Policy)
	if err != nil {
		return result, err
	}
	credentials, err := LoadProtectedGitHubGitCredentials()
	if err != nil {
		return result, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, credentials.Close()) }()
	gitSigning, err := LoadProtectedSSHSigningKey(input.Policy.Signing)
	if err != nil {
		return result, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, gitSigning.Close()) }()
	contentSigner, err := NewSSHContentSigner(gitSigning)
	if err != nil {
		return result, err
	}
	runner := ProtectedGitRunner{Runner: ExecRunner{}, Credentials: credentials}
	stateDir, err := osMkdirPrivateTemp("gardener-release-state-")
	if err != nil {
		return result, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, removePrivateTemp(stateDir)) }()
	store, err := NewProtectedGitStateStore(runner, stateDir, contentSigner, contentSigner, mustSSHWire(contentSigner), CommitIdentity{Name: "gardener-release", Email: input.Policy.Signing.Principal}, RealClock{}, gitSigning)
	if err != nil {
		return result, err
	}
	if err := store.InitWorkingRepository(ctx); err != nil {
		return result, err
	}
	loaded, err := store.LoadState(ctx, validated.Validated.RequestKey)
	if err != nil {
		return result, err
	}
	if loaded.Found {
		return ProductionReservationResult{Record: loaded.Record, StateHead: loaded.RemoteHead, Resumed: true}, nil
	}
	incomplete, err := store.IncompleteOperationsOnLine(ctx, validated.Validated.ReleaseLine)
	if err != nil {
		return result, err
	}
	repositoryDir, err := osMkdirPrivateTemp("gardener-release-resolve-")
	if err != nil {
		return result, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, removePrivateTemp(repositoryDir)) }()
	if _, err := runner.Run(ctx, Command{Path: "git", Args: []string{"init", "--quiet", "-b", "resolve"}, Env: gitStateEnv(), Dir: repositoryDir}); err != nil {
		return result, wrapReleaseError(ErrorClassEvidenceIncomplete, "resolve_repository_init_failed", err)
	}
	refs, source, sourceVersion, err := readProductionResolutionEvidence(ctx, runner, repositoryDir, input.ToolPath, validated.Validated)
	if err != nil {
		return result, err
	}
	existing := make([]ExistingOperation, 0, len(incomplete))
	for _, record := range incomplete {
		existing = append(existing, ExistingOperation{RequestKey: record.Reservation.RequestKey, RequestSHA256: record.Reservation.RequestSHA256, Command: record.Reservation.Command, ReleaseLine: record.Reservation.ReleaseLine, ResolvedVersion: record.Reservation.ResolvedVersion, DevelopmentVersion: record.Reservation.DevelopmentVersion, Phase: string(record.Phase)})
	}
	resolution, err := ResolveVersion(VersionResolutionInput{RequestKey: validated.Validated.RequestKey, RequestSHA256: validated.Validated.RequestSHA256, Command: validated.Validated.Command, RequestedVersion: validated.Validated.Version, ReleaseLine: validated.Validated.ReleaseLine, SourceVersion: sourceVersion, RemoteRefs: refs, ExistingOperations: existing})
	if err != nil {
		return result, err
	}
	reservation, err := BuildReservation(validated, resolution, source, time.Now())
	if err != nil {
		return result, err
	}
	decision, err := ReserveOperation(nil, reservation, incomplete)
	if err != nil {
		return result, err
	}
	decision.Record.WorkflowSHA = input.WorkflowSHA
	decision.Record.ToolSHA = input.ToolSHA
	decision.Record, err = RecordGitSigningIntent(decision.Record, GitSigningIntent{Timestamp: time.Now().Unix(), Message: "release: " + reservation.GenerationVersion, Principal: input.Policy.Signing.Principal, Fingerprint: input.Policy.Signing.Fingerprint})
	if err != nil {
		return result, err
	}
	head, err := store.PersistReservation(ctx, decision, loaded.RemoteHead)
	if err != nil {
		return result, err
	}
	return ProductionReservationResult{Record: decision.Record, StateHead: head}, nil
}

func readProductionResolutionEvidence(ctx context.Context, runner CommandRunner, dir, taggerPath string, request ValidatedRequest) (RemoteRefs, SourceRef, string, error) {
	result, err := runner.Run(ctx, Command{Path: "git", Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "ls-remote", "--heads", "--tags", CanonicalGitHubRemote}, Env: gitStateEnv(), Dir: dir})
	if err != nil {
		return RemoteRefs{}, SourceRef{}, "", wrapReleaseError(ErrorClassEvidenceIncomplete, "remote_refs_read_failed", err)
	}
	refs := RemoteRefs{Complete: true, Branches: map[string]string{}, Tags: map[string]string{}}
	rootVersions := map[string]bool{}
	moduleVersions := map[string]bool{}
	rootTagCommits := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !ValidGitObjectID(fields[0]) {
			return RemoteRefs{}, SourceRef{}, "", newReleaseError(ErrorClassEvidenceIncomplete, "remote_ref_unparseable")
		}
		ref := fields[1]
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			refs.Branches[ref] = fields[0]
		case strings.HasPrefix(ref, "refs/tags/") && strings.HasSuffix(ref, "^{}"):
			name := strings.TrimSuffix(strings.TrimPrefix(ref, "refs/tags/"), "^{}")
			if !strings.Contains(name, "/") {
				rootTagCommits[name] = fields[0]
			}
		case strings.HasPrefix(ref, "refs/tags/"):
			refs.Tags[ref] = fields[0]
			name := strings.TrimPrefix(ref, "refs/tags/")
			version := name
			if index := strings.LastIndex(name, "/"); index >= 0 {
				version = name[index+1:]
				moduleVersions[version] = true
			} else {
				rootVersions[version] = true
			}
		}
	}
	line, err := parseReleaseLine(request.ReleaseLine)
	if err != nil {
		return RemoteRefs{}, SourceRef{}, "", err
	}
	manifestCache := map[string][]string{}
	resolver := func(version, commitSHA string) ([]string, error) {
		key := commitSHA + "\x00" + version
		if cached, ok := manifestCache[key]; ok {
			return append([]string(nil), cached...), nil
		}
		tags, err := deriveTrustedHistoricalTagManifest(ctx, runner, dir, taggerPath, version, commitSHA, line)
		if err != nil {
			return nil, err
		}
		manifestCache[key] = append([]string(nil), tags...)
		return tags, nil
	}
	refs.IncompleteTagVersions, err = trustedIncompleteTagVersions(refs.Tags, rootVersions, moduleVersions, rootTagCommits, line, resolver)
	if err != nil {
		return RemoteRefs{}, SourceRef{}, "", err
	}
	sourceRef := "refs/heads/main"
	if request.Command != "release:prepare" {
		sourceRef = "refs/heads/" + releaseBranchName(line.Major, line.Minor)
	}
	sourceSHA, found := refs.Branches[sourceRef]
	if !found {
		return RemoteRefs{}, SourceRef{}, "", newReleaseError(ErrorClassEvidenceIncomplete, "source_ref_missing")
	}
	if _, err := runner.Run(ctx, Command{Path: "git", Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "fetch", "--quiet", "--no-tags", CanonicalGitHubRemote, sourceSHA}, Env: gitStateEnv(), Dir: dir}); err != nil {
		return RemoteRefs{}, SourceRef{}, "", wrapReleaseError(ErrorClassEvidenceIncomplete, "source_fetch_failed", err)
	}
	versionResult, err := runner.Run(ctx, Command{Path: "git", Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "show", sourceSHA + ":internal/version/version.go"}, Env: gitStateEnv(), Dir: dir})
	if err != nil {
		return RemoteRefs{}, SourceRef{}, "", wrapReleaseError(ErrorClassEvidenceIncomplete, "source_version_read_failed", err)
	}
	match := regexp.MustCompile(`(?m)^var Tag = "([^"]+)"$`).FindStringSubmatch(versionResult.Stdout)
	if match == nil {
		return RemoteRefs{}, SourceRef{}, "", newReleaseError(ErrorClassEvidenceIncomplete, "source_version_missing")
	}
	if _, err := ParseReleaseVersion(match[1]); err != nil {
		return RemoteRefs{}, SourceRef{}, "", err
	}
	return refs, SourceRef{Ref: sourceRef, SHA: sourceSHA}, match[1], nil
}

type historicalTagManifestResolver func(version, commitSHA string) ([]string, error)

func trustedIncompleteTagVersions(tags map[string]string, rootVersions, moduleVersions map[string]bool, rootTagCommits map[string]string, line releaseVersion, resolve historicalTagManifestResolver) ([]string, error) {
	incomplete := map[string]bool{}
	onLine := func(raw string) bool {
		version, err := ParseReleaseVersion(raw)
		return err == nil && version.Major == line.Major && version.Minor == line.Minor
	}
	for version := range moduleVersions {
		if onLine(version) && !rootVersions[version] {
			incomplete[version] = true
		}
	}
	for version := range rootVersions {
		if !onLine(version) {
			continue
		}
		commitSHA := rootTagCommits[version]
		if !ValidGitObjectID(commitSHA) {
			return nil, newReleaseError(ErrorClassEvidenceIncomplete, "root_tag_commit_unavailable")
		}
		expected, err := resolve(version, commitSHA)
		if err != nil {
			return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "tag_manifest_derivation_failed", err)
		}
		if len(expected) == 0 || expected[0] != version {
			return nil, newReleaseError(ErrorClassEvidenceIncomplete, "tag_manifest_derivation_failed")
		}
		for _, name := range expected {
			if _, found := tags["refs/tags/"+name]; !found {
				incomplete[version] = true
				break
			}
		}
	}
	result := make([]string, 0, len(incomplete))
	for version := range incomplete {
		result = append(result, version)
	}
	sort.Strings(result)
	return result, nil
}

func deriveTrustedHistoricalTagManifest(ctx context.Context, runner CommandRunner, dir, taggerPath, version, commitSHA string, line releaseVersion) ([]string, error) {
	if taggerPath == "" || !ValidGitObjectID(commitSHA) {
		return nil, newReleaseError(ErrorClassContractMismatch, "tag_manifest_derivation_invalid")
	}
	if _, err := runner.Run(ctx, Command{Path: "git", Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "fetch", "--quiet", "--no-tags", CanonicalGitHubRemote, commitSHA}, Env: gitStateEnv(), Dir: dir}); err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "tag_manifest_source_fetch_failed", err)
	}
	branch := releaseBranchName(line.Major, line.Minor)
	if _, err := runner.Run(ctx, Command{Path: "git", Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "checkout", "--quiet", "-B", branch, commitSHA}, Env: gitStateEnv(), Dir: dir}); err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "tag_manifest_checkout_failed", err)
	}
	planPath := filepath.Join(dir, ".gardener-release-historical-plan.json")
	defer os.Remove(planPath)
	args := []string{"--root", dir, "--version", version, "--plan-json", planPath, "--untag-modules", "github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2", "--exclude-dirs", "_tools,.claude,.github,tools"}
	if _, err := runner.Run(ctx, Command{Path: taggerPath, Args: args, Env: taggerGenerationEnv(), Dir: dir}); err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "tag_manifest_plan_failed", err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil || validateJSONNoDuplicateKeys(raw, MaxJobArtifactBytes) != nil {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "tag_manifest_plan_invalid")
	}
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "tag_manifest_plan_invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "tag_manifest_plan_invalid")
	}
	manifest, err := PlanManifestFromTaggerOutput(document)
	if err != nil || manifest.SourceSHA != commitSHA || document["branch"] != branch || document["requested_version"] != version {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "tag_manifest_plan_invalid")
	}
	return append([]string(nil), manifest.ExpectedTags...), nil
}

func mustSSHWire(signer *SSHContentSigner) []byte {
	wire, _ := signer.publicWire()
	return wire
}

func osMkdirPrivateTemp(pattern string) (string, error) {
	directory, err := os.MkdirTemp("", pattern)
	if err != nil {
		return "", wrapReleaseError(ErrorClassEvidenceIncomplete, "temporary_directory_failed", err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		_ = os.RemoveAll(directory)
		return "", wrapReleaseError(ErrorClassEvidenceIncomplete, "temporary_directory_failed", err)
	}
	return directory, nil
}

func removePrivateTemp(directory string) error {
	if directory == "" {
		return nil
	}
	return os.RemoveAll(directory)
}
