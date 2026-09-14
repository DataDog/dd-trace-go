// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

func main() {
	os.Exit(run(os.Args[1:], gardenerrelease.OSFileSystem{}, os.Stdout, os.Stderr))
}

func run(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printError(stderr, gardenerrelease.NewCLIError("missing_command"))
		return 2
	}
	switch args[0] {
	case "inspect-request":
		return inspectRequest(args[1:], files, stdout, stderr)
	case "inspect-operation":
		return inspectOperation(args[1:], files, stdout, stderr)
	case "inspect-reconciliation":
		return inspectReconciliation(args[1:], files, stdout, stderr)
	case "validate-request":
		return validateRequest(args[1:], files, stdout, stderr)
	case "reserve-operation":
		return reserveOperation(args[1:], files, stdout, stderr)
	case "generate-unsigned":
		return generateUnsigned(args[1:], files, stdout, stderr)
	case "validate-sign-store":
		return validateSignStore(args[1:], files, stdout, stderr)
	case "publish-branches":
		return publishBranches(args[1:], files, stdout, stderr)
	case "wait-branch-tests":
		return waitBranchTests(args[1:], files, stdout, stderr)
	case "publish-tags":
		return publishTags(args[1:], files, stdout, stderr)
	case "ensure-prepare-pr":
		return ensurePreparePR(args[1:], files, stdout, stderr)
	case "observe-images":
		return observeImages(args[1:], files, stdout, stderr)
	case "record-outcome":
		return recordOutcome(args[1:], files, stdout, stderr)
	case "publish-feedback":
		return publishFeedback(args[1:], files, stdout, stderr)
	default:
		printError(stderr, gardenerrelease.NewCLIError("unknown_command"))
		return 2
	}
}

func inspectRequest(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("inspect-request", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "workflow_dispatch input JSON file")
	policyPath := flags.String("policy", "", "reviewed release policy JSON file")
	if err := flags.Parse(args); err != nil {
		printError(stderr, err)
		return 2
	}
	if *inputPath == "" || *policyPath == "" || flags.NArg() != 0 {
		printError(stderr, gardenerrelease.NewCLIError("invalid_arguments"))
		return 2
	}
	inputRaw, err := gardenerrelease.ReadBoundedFile(files, *inputPath, gardenerrelease.MaxContextBytes+1024)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	policyRaw, err := gardenerrelease.ReadBoundedFile(files, *policyPath, gardenerrelease.MaxPolicyBytes)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	inspection, err := gardenerrelease.InspectRequest(inputRaw, policyRaw)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writeJSON(stdout, stderr, inspection)
}

func inspectOperation(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("inspect-operation", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "operation evidence JSON file")
	if err := flags.Parse(args); err != nil {
		printError(stderr, err)
		return 2
	}
	if *inputPath == "" || flags.NArg() != 0 {
		printError(stderr, gardenerrelease.NewCLIError("invalid_arguments"))
		return 2
	}
	inputRaw, err := gardenerrelease.ReadBoundedFile(files, *inputPath, gardenerrelease.MaxContextBytes)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	inspection, err := gardenerrelease.InspectOperation(inputRaw)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writeJSON(stdout, stderr, inspection)
}

func inspectReconciliation(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("inspect-reconciliation", flag.ContinueOnError)
	flags.SetOutput(stderr)
	requestPath := flags.String("request", "", "original workflow_dispatch input JSON file")
	operationPath := flags.String("operation", "", "optional operation evidence JSON file")
	if err := flags.Parse(args); err != nil {
		printError(stderr, err)
		return 2
	}
	if *requestPath == "" || flags.NArg() != 0 {
		printError(stderr, gardenerrelease.NewCLIError("invalid_arguments"))
		return 2
	}
	requestRaw, err := gardenerrelease.ReadBoundedFile(files, *requestPath, gardenerrelease.MaxContextBytes+1024)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	var operationRaw []byte
	if *operationPath != "" {
		operationRaw, err = gardenerrelease.ReadBoundedFile(files, *operationPath, gardenerrelease.MaxContextBytes)
		if err != nil {
			printError(stderr, err)
			return 1
		}
	}
	inspection, err := gardenerrelease.InspectReconciliation(requestRaw, operationRaw)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writeJSON(stdout, stderr, inspection)
}

func validateRequest(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("validate-request", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dispatchPath := flags.String("dispatch", "", "strict workflow dispatch JSON")
	policyPath := flags.String("policy", "", "reviewed policy JSON")
	if err := flags.Parse(args); err != nil || *dispatchPath == "" || *policyPath == "" || flags.NArg() != 0 {
		printError(stderr, gardenerrelease.NewCLIError("invalid_arguments"))
		return 2
	}
	dispatchRaw, policyRaw, err := readDispatchAndPolicy(files, *dispatchPath, *policyPath)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	policy, err := gardenerrelease.DecodePolicy(policyRaw)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	token := os.Getenv("GARDENER_RELEASE_READ_TOKEN")
	if token == "" {
		printError(stderr, gardenerrelease.NewCLIError("read_token_unavailable"))
		return 1
	}
	validated, err := gardenerrelease.ValidateDispatchFromGitHub(context.Background(), gardenerrelease.NewProductionGitHubClient(token), dispatchRaw, policy)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	selector, err := gardenerrelease.LoadProductionOperationSelector(context.Background(), validated.Validated, policy)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	payload, _ := json.Marshal(map[string]any{"request": validated.Request, "validated_request": validated.Validated, "selector": selector})
	artifact, err := newRuntimeArtifact(gardenerrelease.JobValidateRequest, validated.Validated.RequestKey, "", payload)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writeJSON(stdout, stderr, artifact)
}

func reserveOperation(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("reserve-operation", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dispatchPath := flags.String("dispatch", "", "strict workflow dispatch JSON")
	policyPath := flags.String("policy", "", "reviewed policy JSON")
	validatedPath := flags.String("validated", "", "validated request artifact")
	workflowSHA := flags.String("workflow-sha", "", "trusted workflow revision")
	toolSHA := flags.String("tool-sha", "", "trusted tool revision")
	taggerPath := flags.String("tagger", "", "trusted tagger binary")
	if err := flags.Parse(args); err != nil || *dispatchPath == "" || *policyPath == "" || *validatedPath == "" || *taggerPath == "" || flags.NArg() != 0 {
		printError(stderr, gardenerrelease.NewCLIError("invalid_arguments"))
		return 2
	}
	dispatchRaw, policyRaw, err := readDispatchAndPolicy(files, *dispatchPath, *policyPath)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	validatedRaw, err := gardenerrelease.ReadBoundedFile(files, *validatedPath, gardenerrelease.MaxJobArtifactBytes)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	prior, err := gardenerrelease.DecodeJobArtifact(validatedRaw, gardenerrelease.JobValidateRequest)
	if err != nil || validateRuntimeArtifact(prior, false) != nil {
		printError(stderr, gardenerrelease.NewCLIError("job_artifact_runtime_mismatch"))
		return 1
	}
	var validatedPayload struct {
		Request          gardenerrelease.DispatchRequest   `json:"request"`
		ValidatedRequest gardenerrelease.ValidatedRequest  `json:"validated_request"`
		Selector         gardenerrelease.OperationSelector `json:"selector"`
	}
	if gardenerrelease.DecodeJobPayload(prior, &validatedPayload) != nil || validatedPayload.Selector.NextJob != gardenerrelease.JobReserveOperation || validatedPayload.Selector.RecordFound {
		printError(stderr, gardenerrelease.NewCLIError("selector_artifact_mismatch"))
		return 1
	}
	policy, err := gardenerrelease.DecodePolicy(policyRaw)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	result, err := gardenerrelease.ReserveProductionOperation(context.Background(), gardenerrelease.ProductionReservationInput{DispatchJSON: dispatchRaw, Policy: policy, WorkflowSHA: *workflowSHA, ToolSHA: *toolSHA, ToolPath: *taggerPath})
	if err != nil {
		printError(stderr, err)
		return 1
	}
	if result.Record.Reservation.RequestKey != prior.RequestKey {
		printError(stderr, gardenerrelease.NewCLIError("job_artifact_request_mismatch"))
		return 1
	}
	payload, _ := json.Marshal(map[string]any{"record": result.Record, "state_head": result.StateHead, "decision": map[string]bool{"resumed": result.Resumed}})
	artifact, err := newRuntimeArtifact(gardenerrelease.JobReserveOperation, prior.RequestKey, prior.ArtifactSHA256, payload)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writeJSON(stdout, stderr, artifact)
}

func generateUnsigned(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("generate-unsigned", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reservedPath := flags.String("reserved", "", "reserved-operation artifact")
	selectorPath := flags.String("selector", "", "validated selector artifact for a reserved resume")
	policyPath := flags.String("policy", "", "reviewed policy JSON for a reserved resume")
	sourceRemote := flags.String("source-remote", "", "local read-only source checkout")
	tagger := flags.String("tagger", "", "trusted tagger binary")
	workDir := flags.String("workdir", "", "empty generation directory")
	bundlePath := flags.String("bundle-output", "", "fixed unsigned bundle output")
	cacheDir := flags.String("cache-dir", "", "isolated cache directory outside checkout")
	if err := flags.Parse(args); err != nil || ((*reservedPath == "") == (*selectorPath == "")) || (*selectorPath != "" && *policyPath == "") || *sourceRemote == "" || *tagger == "" || *workDir == "" || *bundlePath == "" || *cacheDir == "" || flags.NArg() != 0 {
		printError(stderr, gardenerrelease.NewCLIError("invalid_arguments"))
		return 2
	}
	var prior gardenerrelease.JobArtifact
	var record gardenerrelease.Record
	if *selectorPath != "" {
		selectorArtifact, policy, selector, ok := readSelectorInput(files, *selectorPath, *policyPath, gardenerrelease.JobGenerateUnsigned, stderr)
		if !ok || !selector.RecordFound || selector.Phase != gardenerrelease.PhaseReserved {
			return 1
		}
		loaded, err := gardenerrelease.LoadProductionStateReadOnly(context.Background(), selector.Record, selector.StateHead, policy)
		if err != nil {
			printError(stderr, err)
			return 1
		}
		prior, record = selectorArtifact, loaded
	} else {
		raw, err := gardenerrelease.ReadBoundedFile(files, *reservedPath, gardenerrelease.MaxJobArtifactBytes)
		if err != nil {
			printError(stderr, err)
			return 1
		}
		prior, err = gardenerrelease.DecodeJobArtifact(raw, gardenerrelease.JobReserveOperation)
		if err != nil || validateRuntimePredecessor(prior) != nil {
			printError(stderr, gardenerrelease.NewCLIError("job_artifact_runtime_mismatch"))
			return 1
		}
		var payload struct {
			Record    gardenerrelease.Record `json:"record"`
			StateHead string                 `json:"state_head"`
			Decision  map[string]bool        `json:"decision"`
		}
		if err := gardenerrelease.DecodeJobPayload(prior, &payload); err != nil {
			printError(stderr, err)
			return 1
		}
		record = payload.Record
	}
	if record.Phase != gardenerrelease.PhaseReserved || record.Reservation.RequestKey != prior.RequestKey || len(record.Reservation.SourceRefs) != 1 {
		printError(stderr, gardenerrelease.NewCLIError("generation_record_invalid"))
		return 1
	}
	target := ""
	if record.Reservation.Command == "release:prepare" && len(record.Reservation.BranchIntents) == 2 {
		target = record.Reservation.BranchIntents[1].Ref
	} else if len(record.Reservation.BranchIntents) == 1 {
		target = record.Reservation.BranchIntents[0].Ref
	}
	const heads = "refs/heads/"
	if len(target) <= len(heads) || target[:len(heads)] != heads {
		printError(stderr, gardenerrelease.NewCLIError("generation_record_invalid"))
		return 1
	}
	absWork, workErr := filepath.Abs(*workDir)
	absCache, cacheErr := filepath.Abs(*cacheDir)
	relCache, relErr := filepath.Rel(absWork, absCache)
	if workErr != nil || cacheErr != nil || relErr != nil || relCache == "." || !strings.HasPrefix(relCache, ".."+string(os.PathSeparator)) {
		printError(stderr, gardenerrelease.NewCLIError("invalid_cache_directory"))
		return 1
	}
	output, err := gardenerrelease.Generate(context.Background(), gardenerrelease.ExecRunner{}, gardenerrelease.GenerateInput{SourceRemotePath: *sourceRemote, SourceSHA: record.Reservation.SourceRefs[0].SHA, TargetBranch: target[len(heads):], ResolvedVersion: record.Reservation.GenerationVersion, UntaggedModules: []string{"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2"}, ExcludedDirs: []string{"_tools", ".claude", ".github", "tools"}, TaggerBinaryPath: *tagger, WorkDir: *workDir, CacheDirs: gardenerrelease.TaggerCacheDirs{GOPATH: *cacheDir + "/gopath", GOCACHE: *cacheDir + "/gocache", GOMODCACHE: *cacheDir + "/gomodcache"}})
	if err != nil {
		printError(stderr, err)
		return 1
	}
	manifest, err := gardenerrelease.PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	bundleDigest, err := gardenerrelease.BuildUnsignedRepositoryBundle(context.Background(), gardenerrelease.ExecRunner{}, *workDir, *bundlePath, output)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	artifactPayload, _ := json.Marshal(map[string]any{"record": record, "generation": output, "manifest": manifest, "repository_bundle_sha256": bundleDigest})
	artifact, err := newRuntimeArtifact(gardenerrelease.JobGenerateUnsigned, prior.RequestKey, prior.ArtifactSHA256, artifactPayload)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writeJSON(stdout, stderr, artifact)
}

func validateSignStore(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("validate-sign-store", flag.ContinueOnError)
	flags.SetOutput(stderr)
	generationPath := flags.String("generation", "", "generation artifact")
	reservedPath := flags.String("reserved", "", "reservation artifact")
	selectorPath := flags.String("selector", "", "validated selector artifact for a reserved resume")
	bundlePath := flags.String("bundle", "", "unsigned repository bundle")
	policyPath := flags.String("policy", "", "reviewed policy JSON")
	toolPath := flags.String("tool", "", "trusted release tool binary")
	validatorPath := flags.String("validator", "", "trusted validator binary")
	if err := flags.Parse(args); err != nil || *generationPath == "" || ((*reservedPath == "") == (*selectorPath == "")) || *bundlePath == "" || *policyPath == "" || *toolPath == "" || *validatorPath == "" || flags.NArg() != 0 {
		printError(stderr, gardenerrelease.NewCLIError("invalid_arguments"))
		return 2
	}
	generationRaw, err := gardenerrelease.ReadBoundedFile(files, *generationPath, gardenerrelease.MaxJobArtifactBytes)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	generationArtifact, err := gardenerrelease.DecodeJobArtifact(generationRaw, gardenerrelease.JobGenerateUnsigned)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	var reservedArtifact gardenerrelease.JobArtifact
	var reservedRecord gardenerrelease.Record
	var reservedHead string
	if *selectorPath != "" {
		selectorArtifact, _, selector, ok := readSelectorInput(files, *selectorPath, *policyPath, gardenerrelease.JobGenerateUnsigned, stderr)
		if !ok || selector.Phase != gardenerrelease.PhaseReserved {
			return 1
		}
		reservedArtifact, reservedRecord, reservedHead = selectorArtifact, selector.Record, selector.StateHead
	} else {
		reservedRaw, readErr := gardenerrelease.ReadBoundedFile(files, *reservedPath, gardenerrelease.MaxJobArtifactBytes)
		if readErr != nil {
			printError(stderr, readErr)
			return 1
		}
		reservedArtifact, err = gardenerrelease.DecodeJobArtifact(reservedRaw, gardenerrelease.JobReserveOperation)
		if err != nil {
			printError(stderr, err)
			return 1
		}
		var reservedPayload struct {
			Record    gardenerrelease.Record `json:"record"`
			StateHead string                 `json:"state_head"`
			Decision  map[string]bool        `json:"decision"`
		}
		if err := gardenerrelease.DecodeJobPayload(reservedArtifact, &reservedPayload); err != nil {
			printError(stderr, err)
			return 1
		}
		reservedRecord, reservedHead = reservedPayload.Record, reservedPayload.StateHead
	}
	if generationArtifact.PreviousSHA256 != reservedArtifact.ArtifactSHA256 || generationArtifact.RequestKey != reservedArtifact.RequestKey {
		printError(stderr, gardenerrelease.NewCLIError("job_artifact_chain_mismatch"))
		return 1
	}
	var generationPayload struct {
		Record                 gardenerrelease.Record           `json:"record"`
		Generation             gardenerrelease.GenerationOutput `json:"generation"`
		Manifest               gardenerrelease.PlanManifest     `json:"manifest"`
		RepositoryBundleSHA256 string                           `json:"repository_bundle_sha256"`
	}
	if err := gardenerrelease.DecodeJobPayload(generationArtifact, &generationPayload); err != nil {
		printError(stderr, err)
		return 1
	}
	if !sameRecord(generationPayload.Record, reservedRecord) {
		printError(stderr, gardenerrelease.NewCLIError("job_artifact_state_mismatch"))
		return 1
	}
	policyRaw, err := gardenerrelease.ReadBoundedFile(files, *policyPath, gardenerrelease.MaxPolicyBytes)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	policy, err := gardenerrelease.DecodePolicy(policyRaw)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	result, err := gardenerrelease.ValidateSignAndStoreProduction(context.Background(), gardenerrelease.ProductionSignInput{Record: generationPayload.Record, ExpectedStateHead: reservedHead, Generation: generationPayload.Generation, Manifest: generationPayload.Manifest, GenerationBundle: *bundlePath, GenerationSHA256: generationPayload.RepositoryBundleSHA256, ToolPath: *toolPath, ValidatorPath: *validatorPath, Policy: policy, GenerationEvidence: generationArtifact})
	if err != nil {
		printError(stderr, err)
		return 1
	}
	payload, _ := json.Marshal(map[string]any{"record": result.Record, "state_head": result.StateHead, "signed_output": result.SignedOutput})
	artifact, err := newRuntimeArtifact(gardenerrelease.JobValidateSignStore, generationArtifact.RequestKey, generationArtifact.ArtifactSHA256, payload)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writeJSON(stdout, stderr, artifact)
}

func sameRecord(left, right gardenerrelease.Record) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func publishBranches(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("publish-branches", flag.ContinueOnError)
	flags.SetOutput(stderr)
	signedPath := flags.String("signed", "", "signed-state artifact")
	selectorPath := flags.String("selector", "", "validated selector artifact for a signed resume")
	policyPath := flags.String("policy", "", "reviewed policy JSON")
	if err := flags.Parse(args); err != nil || ((*signedPath == "") == (*selectorPath == "")) || *policyPath == "" || flags.NArg() != 0 {
		printError(stderr, gardenerrelease.NewCLIError("invalid_arguments"))
		return 2
	}
	var artifact gardenerrelease.JobArtifact
	var record gardenerrelease.Record
	var stateHead string
	var policy gardenerrelease.Policy
	if *selectorPath != "" {
		selectorArtifact, selectedPolicy, selector, ok := readSelectorInput(files, *selectorPath, *policyPath, gardenerrelease.JobPublishBranches, stderr)
		if !ok || selector.Phase != gardenerrelease.PhaseSigned {
			return 1
		}
		artifact, policy, record, stateHead = selectorArtifact, selectedPolicy, selector.Record, selector.StateHead
	} else {
		var err error
		artifact, err = readJobArtifact(files, *signedPath, gardenerrelease.JobValidateSignStore)
		if err != nil {
			printError(stderr, err)
			return 1
		}
		var input struct {
			Record       gardenerrelease.Record       `json:"record"`
			StateHead    string                       `json:"state_head"`
			SignedOutput gardenerrelease.SignedOutput `json:"signed_output"`
		}
		if err := gardenerrelease.DecodeJobPayload(artifact, &input); err != nil || input.Record.SignedOutput == nil || input.SignedOutput.ReleaseSHA != input.Record.SignedOutput.ReleaseSHA {
			printError(stderr, gardenerrelease.NewCLIError("job_artifact_state_mismatch"))
			return 1
		}
		record, stateHead = input.Record, input.StateHead
		policy, err = readPolicy(files, *policyPath)
		if err != nil {
			printError(stderr, err)
			return 1
		}
	}
	var err error
	if policy.Revision == "" {
		policy, err = readPolicy(files, *policyPath)
	}
	if err != nil {
		printError(stderr, err)
		return 1
	}
	result, err := gardenerrelease.PublishProductionBranches(context.Background(), gardenerrelease.ProductionPublicationInput{Record: record, ExpectedStateHead: stateHead, Policy: policy})
	if err != nil {
		printError(stderr, err)
		return 1
	}
	payload, _ := json.Marshal(map[string]any{"record": result.Record, "state_head": result.StateHead, "publication": result.Publication})
	output, err := newRuntimeArtifact(gardenerrelease.JobPublishBranches, artifact.RequestKey, artifact.ArtifactSHA256, payload)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writeJSON(stdout, stderr, output)
}

func waitBranchTests(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("wait-branch-tests", flag.ContinueOnError)
	flags.SetOutput(stderr)
	priorPath := flags.String("branches", "", "publish-branches artifact")
	selectorPath := flags.String("selector", "", "validated selector artifact for a branches-published resume")
	policyPath := flags.String("policy", "", "reviewed policy JSON")
	if flags.Parse(args) != nil || ((*priorPath == "") == (*selectorPath == "")) || *policyPath == "" || flags.NArg() != 0 {
		return argumentError(stderr)
	}
	var prior gardenerrelease.JobArtifact
	var policy gardenerrelease.Policy
	var record gardenerrelease.Record
	var head string
	var ok bool
	if *selectorPath != "" {
		selectorArtifact, selectedPolicy, selector, selected := readSelectorInput(files, *selectorPath, *policyPath, gardenerrelease.JobWaitBranchTests, stderr)
		if !selected || selector.Phase != gardenerrelease.PhaseBranchesPublished {
			return 1
		}
		prior, policy, record, head, ok = selectorArtifact, selectedPolicy, selector.Record, selector.StateHead, true
	} else {
		prior, policy, record, head, ok = readPhaseInput(files, *priorPath, *policyPath, gardenerrelease.JobPublishBranches, stderr)
	}
	if !ok {
		return 1
	}
	if record.Phase != gardenerrelease.PhaseBranchesPublished {
		printError(stderr, gardenerrelease.NewCLIError("job_artifact_phase_mismatch"))
		return 1
	}
	result, err := gardenerrelease.WaitProductionBranchTests(context.Background(), gardenerrelease.ProductionPublicationInput{Record: record, ExpectedStateHead: head, Policy: policy})
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writePhaseArtifact(stdout, stderr, gardenerrelease.JobWaitBranchTests, prior, map[string]any{"record": result.Record, "state_head": result.StateHead, "test_evidence": result.Evidence})
}

func publishTags(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("publish-tags", flag.ContinueOnError)
	flags.SetOutput(stderr)
	priorPath := flags.String("tests", "", "wait-branch-tests artifact")
	selectorPath := flags.String("selector", "", "validated selector artifact for a tests-passed resume")
	policyPath := flags.String("policy", "", "reviewed policy JSON")
	if flags.Parse(args) != nil || ((*priorPath == "") == (*selectorPath == "")) || *policyPath == "" || flags.NArg() != 0 {
		return argumentError(stderr)
	}
	var prior gardenerrelease.JobArtifact
	var policy gardenerrelease.Policy
	var record gardenerrelease.Record
	var head string
	var evidence gardenerrelease.VerifiedTestEvidence
	if *selectorPath != "" {
		selectorArtifact, selectedPolicy, selector, ok := readSelectorInput(files, *selectorPath, *policyPath, gardenerrelease.JobPublishTags, stderr)
		if !ok || selector.Phase != gardenerrelease.PhaseTestsPassed {
			return 1
		}
		var found bool
		evidence, found = gardenerrelease.LatestStoredTestEvidence(selector.Record.Events)
		if !found {
			printError(stderr, gardenerrelease.NewCLIError("stored_test_evidence_invalid"))
			return 1
		}
		prior, policy, record, head = selectorArtifact, selectedPolicy, selector.Record, selector.StateHead
	} else {
		var ok bool
		prior, policy, record, head, ok = readPhaseInput(files, *priorPath, *policyPath, gardenerrelease.JobWaitBranchTests, stderr)
		if !ok || record.Phase != gardenerrelease.PhaseBranchesPublished {
			return 1
		}
		var payload struct {
			Record       gardenerrelease.Record               `json:"record"`
			StateHead    string                               `json:"state_head"`
			TestEvidence gardenerrelease.VerifiedTestEvidence `json:"test_evidence"`
		}
		if gardenerrelease.DecodeJobPayload(prior, &payload) != nil || !sameRecord(payload.Record, record) {
			printError(stderr, gardenerrelease.NewCLIError("job_artifact_state_mismatch"))
			return 1
		}
		evidence = payload.TestEvidence
	}
	result, err := gardenerrelease.PublishProductionTags(context.Background(), gardenerrelease.ProductionPublicationInput{Record: record, ExpectedStateHead: head, Policy: policy}, evidence)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writePhaseArtifact(stdout, stderr, gardenerrelease.JobPublishTags, prior, map[string]any{"record": result.Record, "state_head": result.StateHead, "publication": result.Publication})
}

func ensurePreparePR(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ensure-prepare-pr", flag.ContinueOnError)
	flags.SetOutput(stderr)
	priorPath := flags.String("tags", "", "publish-tags artifact")
	selectorPath := flags.String("selector", "", "validated selector artifact for a tags-published prepare resume")
	policyPath := flags.String("policy", "", "reviewed policy JSON")
	if flags.Parse(args) != nil || ((*priorPath == "") == (*selectorPath == "")) || *policyPath == "" || flags.NArg() != 0 {
		return argumentError(stderr)
	}
	var prior gardenerrelease.JobArtifact
	var policy gardenerrelease.Policy
	var record gardenerrelease.Record
	var head string
	var ok bool
	if *selectorPath != "" {
		selectorArtifact, selectedPolicy, selector, selected := readSelectorInput(files, *selectorPath, *policyPath, gardenerrelease.JobEnsurePreparePR, stderr)
		if !selected || selector.Phase != gardenerrelease.PhaseTagsPublished {
			return 1
		}
		prior, policy, record, head, ok = selectorArtifact, selectedPolicy, selector.Record, selector.StateHead, true
	} else {
		prior, policy, record, head, ok = readPhaseInput(files, *priorPath, *policyPath, gardenerrelease.JobPublishTags, stderr)
	}
	if !ok {
		return 1
	}
	if record.Reservation.Command != "release:prepare" || record.Phase != gardenerrelease.PhaseTagsPublished {
		printError(stderr, gardenerrelease.NewCLIError("prepare_pr_not_applicable"))
		return 1
	}
	result, err := gardenerrelease.EnsureProductionPreparePR(context.Background(), gardenerrelease.ProductionPublicationInput{Record: record, ExpectedStateHead: head, Policy: policy})
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writePhaseArtifact(stdout, stderr, gardenerrelease.JobEnsurePreparePR, prior, map[string]any{"record": result.Record, "state_head": result.StateHead, "pr_number": result.PRNumber, "outcome": result.Outcome})
}

func observeImages(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("observe-images", flag.ContinueOnError)
	flags.SetOutput(stderr)
	priorPath := flags.String("tags", "", "publish-tags artifact")
	selectorPath := flags.String("selector", "", "validated selector artifact for a tags-published release resume")
	policyPath := flags.String("policy", "", "reviewed policy JSON")
	if flags.Parse(args) != nil || ((*priorPath == "") == (*selectorPath == "")) || *policyPath == "" || flags.NArg() != 0 {
		return argumentError(stderr)
	}
	var prior gardenerrelease.JobArtifact
	var policy gardenerrelease.Policy
	var record gardenerrelease.Record
	var head string
	var ok bool
	if *selectorPath != "" {
		selectorArtifact, selectedPolicy, selector, selected := readSelectorInput(files, *selectorPath, *policyPath, gardenerrelease.JobObserveImages, stderr)
		if !selected || selector.Phase != gardenerrelease.PhaseTagsPublished {
			return 1
		}
		prior, policy, record, head, ok = selectorArtifact, selectedPolicy, selector.Record, selector.StateHead, true
	} else {
		prior, policy, record, head, ok = readPhaseInput(files, *priorPath, *policyPath, gardenerrelease.JobPublishTags, stderr)
	}
	if !ok {
		return 1
	}
	if record.Reservation.Command != "release:release" || record.Phase != gardenerrelease.PhaseTagsPublished {
		printError(stderr, gardenerrelease.NewCLIError("images_not_applicable"))
		return 1
	}
	if _, err := gardenerrelease.LoadProductionStateReadOnly(context.Background(), record, head, policy); err != nil {
		printError(stderr, err)
		return 1
	}
	token := os.Getenv("GARDENER_RELEASE_READ_TOKEN")
	if token == "" {
		printError(stderr, gardenerrelease.NewCLIError("read_token_unavailable"))
		return 1
	}
	evidence, outcome, err := gardenerrelease.ObserveReleaseImages(context.Background(), gardenerrelease.NewProductionGitHubClient(token), policy.Image, record)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writePhaseArtifact(stdout, stderr, gardenerrelease.JobObserveImages, prior, map[string]any{"record": record, "state_head": head, "image_evidence": evidence, "outcome": outcome})
}

func recordOutcome(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("record-outcome", flag.ContinueOnError)
	flags.SetOutput(stderr)
	tagsPath := flags.String("tags", "", "publish-tags artifact for promote")
	preparePath := flags.String("prepare", "", "ensure-prepare-pr artifact")
	imagesPath := flags.String("images", "", "observe-images artifact")
	selectorPath := flags.String("selector", "", "validated selector artifact for a tags-published promote resume")
	policyPath := flags.String("policy", "", "reviewed policy JSON")
	if flags.Parse(args) != nil || *policyPath == "" || flags.NArg() != 0 {
		return argumentError(stderr)
	}
	set := 0
	for _, value := range []string{*tagsPath, *preparePath, *imagesPath, *selectorPath} {
		if value != "" {
			set++
		}
	}
	if set != 1 {
		return argumentError(stderr)
	}
	path, job := *tagsPath, gardenerrelease.JobPublishTags
	if *preparePath != "" {
		path, job = *preparePath, gardenerrelease.JobEnsurePreparePR
	}
	if *imagesPath != "" {
		path, job = *imagesPath, gardenerrelease.JobObserveImages
	}
	var prior gardenerrelease.JobArtifact
	var policy gardenerrelease.Policy
	var record gardenerrelease.Record
	var head string
	var ok bool
	if *selectorPath != "" {
		selectorArtifact, selectedPolicy, selector, selected := readSelectorInput(files, *selectorPath, *policyPath, gardenerrelease.JobRecordOutcome, stderr)
		if !selected || selector.Phase != gardenerrelease.PhaseTagsPublished || selector.Command != "release:promote" {
			return 1
		}
		prior, policy, record, head, ok, job = selectorArtifact, selectedPolicy, selector.Record, selector.StateHead, true, gardenerrelease.JobPublishTags
	} else {
		prior, policy, record, head, ok = readPhaseInput(files, path, *policyPath, job, stderr)
	}
	if !ok {
		return 1
	}
	outcome := gardenerrelease.OperationOutcome{SchemaVersion: "1", Publication: gardenerrelease.PublicationOutcome{Status: gardenerrelease.WorkSucceeded, ReleaseSHA: record.SignedOutput.ReleaseSHA}}
	switch job {
	case gardenerrelease.JobPublishTags:
		if record.Reservation.Command != "release:promote" {
			printError(stderr, gardenerrelease.NewCLIError("outcome_predecessor_mismatch"))
			return 1
		}
		outcome.PreparePR.Status, outcome.Images = gardenerrelease.WorkNotApplicable, gardenerrelease.WorkNotApplicable
	case gardenerrelease.JobEnsurePreparePR:
		var value struct {
			Record    gardenerrelease.Record      `json:"record"`
			StateHead string                      `json:"state_head"`
			PRNumber  string                      `json:"pr_number"`
			Outcome   gardenerrelease.WorkOutcome `json:"outcome"`
		}
		if gardenerrelease.DecodeJobPayload(prior, &value) != nil || value.Outcome != gardenerrelease.WorkSucceeded {
			printError(stderr, gardenerrelease.NewCLIError("outcome_predecessor_mismatch"))
			return 1
		}
		outcome.PreparePR, outcome.Images = gardenerrelease.PreparePROutcome{Status: value.Outcome, Number: value.PRNumber}, gardenerrelease.WorkNotApplicable
	case gardenerrelease.JobObserveImages:
		var value struct {
			Record        gardenerrelease.Record                   `json:"record"`
			StateHead     string                                   `json:"state_head"`
			ImageEvidence []gardenerrelease.ImagePromotionEvidence `json:"image_evidence"`
			Outcome       gardenerrelease.WorkOutcome              `json:"outcome"`
		}
		if gardenerrelease.DecodeJobPayload(prior, &value) != nil {
			printError(stderr, gardenerrelease.NewCLIError("outcome_predecessor_mismatch"))
			return 1
		}
		outcome.PreparePR.Status, outcome.Images, outcome.ImageEvidence = gardenerrelease.WorkNotApplicable, value.Outcome, value.ImageEvidence
	}
	result, err := gardenerrelease.RecordProductionOutcome(context.Background(), gardenerrelease.ProductionPublicationInput{Record: record, ExpectedStateHead: head, Policy: policy}, outcome)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writePhaseArtifact(stdout, stderr, gardenerrelease.JobRecordOutcome, prior, map[string]any{"record": result.Record, "state_head": result.StateHead, "outcome": result.Outcome})
}

func publishFeedback(args []string, files gardenerrelease.FileSystem, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("publish-feedback", flag.ContinueOnError)
	flags.SetOutput(stderr)
	priorPath := flags.String("outcome", "", "record-outcome artifact")
	selectorPath := flags.String("selector", "", "validated selector artifact for complete feedback")
	policyPath := flags.String("policy", "", "reviewed policy JSON")
	if flags.Parse(args) != nil || ((*priorPath == "") == (*selectorPath == "")) || *policyPath == "" || flags.NArg() != 0 {
		return argumentError(stderr)
	}
	var prior gardenerrelease.JobArtifact
	var policy gardenerrelease.Policy
	var value struct {
		Record    gardenerrelease.Record           `json:"record"`
		StateHead string                           `json:"state_head"`
		Outcome   gardenerrelease.OperationOutcome `json:"outcome"`
	}
	recordFound := true
	if *selectorPath != "" {
		selectorArtifact, selectedPolicy, selector, ok := readSelectorInput(files, *selectorPath, *policyPath, "", stderr)
		if !ok {
			return 1
		}
		var outcome gardenerrelease.OperationOutcome
		if selector.RecordFound {
			if selector.Phase == gardenerrelease.PhaseComplete {
				var found bool
				outcome, found = gardenerrelease.LatestOperationOutcome(selector.Record)
				if !found {
					printError(stderr, gardenerrelease.NewCLIError("outcome_predecessor_mismatch"))
					return 1
				}
			}
			value.Record, value.StateHead = selector.Record, selector.StateHead
		} else {
			var selectorValue struct {
				Request          gardenerrelease.DispatchRequest  `json:"request"`
				ValidatedRequest gardenerrelease.ValidatedRequest `json:"validated_request"`
			}
			if gardenerrelease.DecodeJobPayload(selectorArtifact, &selectorValue) != nil {
				printError(stderr, gardenerrelease.NewCLIError("selector_artifact_mismatch"))
				return 1
			}
			var err error
			value.Record, err = gardenerrelease.FeedbackRecordForUnreservedRequest(selectorValue.Request, selectorValue.ValidatedRequest)
			if err != nil {
				printError(stderr, err)
				return 1
			}
			recordFound = false
		}
		prior, policy = selectorArtifact, selectedPolicy
		value.Outcome = outcome
	} else {
		var err error
		prior, err = readJobArtifact(files, *priorPath, gardenerrelease.JobRecordOutcome)
		if err != nil || validateRuntimePredecessor(prior) != nil {
			printError(stderr, gardenerrelease.NewCLIError("job_artifact_runtime_mismatch"))
			return 1
		}
		policy, err = readPolicy(files, *policyPath)
		if err != nil {
			printError(stderr, err)
			return 1
		}
		if gardenerrelease.DecodeJobPayload(prior, &value) != nil || value.Record.Reservation.RequestKey != prior.RequestKey || value.Record.SignedOutput == nil || !gardenerrelease.ValidGitObjectID(value.StateHead) {
			printError(stderr, gardenerrelease.NewCLIError("job_artifact_state_mismatch"))
			return 1
		}
	}
	if recordFound {
		loadedRecord, err := gardenerrelease.LoadProductionStateReadOnly(context.Background(), value.Record, value.StateHead, policy)
		if err != nil {
			printError(stderr, err)
			return 1
		}
		value.Record = loadedRecord
		stored, found := gardenerrelease.LatestOperationOutcome(value.Record)
		storedJSON, _ := json.Marshal(stored)
		inputJSON, _ := json.Marshal(value.Outcome)
		if (*selectorPath == "" || value.Record.Phase == gardenerrelease.PhaseComplete) && (!found || string(storedJSON) != string(inputJSON)) {
			printError(stderr, gardenerrelease.NewCLIError("outcome_predecessor_mismatch"))
			return 1
		}
	}
	token := os.Getenv("GARDENER_RELEASE_ISSUES_TOKEN")
	if token == "" || os.Getenv(gardenerrelease.PublicationTokenEnvironment) != "" || os.Getenv(gardenerrelease.ProtectedSSHSigningKeyEnvironment) != "" {
		printError(stderr, gardenerrelease.NewCLIError("feedback_credential_boundary"))
		return 1
	}
	state := gardenerrelease.FeedbackComplete
	if !recordFound {
		state = gardenerrelease.FeedbackPending
	} else if *selectorPath != "" && value.Record.Phase != gardenerrelease.PhaseComplete {
		switch value.Record.Phase {
		case gardenerrelease.PhaseReserved, gardenerrelease.PhaseSigned:
			state = gardenerrelease.FeedbackPending
		case gardenerrelease.PhaseBranchesPublished:
			state = gardenerrelease.FeedbackTesting
		case gardenerrelease.PhaseTestsPassed:
			state = gardenerrelease.FeedbackPartialPublication
		case gardenerrelease.PhaseTagsPublished:
			state = gardenerrelease.FeedbackTagsPublished
		default:
			printError(stderr, gardenerrelease.NewCLIError("feedback_state_invalid"))
			return 1
		}
	} else if value.Outcome.Images == gardenerrelease.WorkPending {
		state = gardenerrelease.FeedbackImagesPending
	} else if value.Outcome.Images == gardenerrelease.WorkFailed {
		state = gardenerrelease.FeedbackImagesFailed
	}
	pr := value.Outcome.PreparePR.Number
	releaseSHA := ""
	if value.Record.SignedOutput != nil {
		releaseSHA = value.Record.SignedOutput.ReleaseSHA
	}
	result, err := gardenerrelease.PublishFeedback(context.Background(), gardenerrelease.NewProductionGitHubClient(token), gardenerrelease.FeedbackRequest{Record: value.Record, Identity: policy.FeedbackIdentity, State: state, Version: value.Record.Reservation.ResolvedVersion, ReleaseSHA: releaseSHA, PRNumber: pr, WorkflowRun: prior.WorkflowRunID})
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writePhaseArtifact(stdout, stderr, gardenerrelease.JobFeedback, prior, map[string]any{"record": value.Record, "feedback": result, "outcome": value.Outcome})
}

func readSelectorInput(files gardenerrelease.FileSystem, artifactPath, policyPath string, next gardenerrelease.OrchestrationJob, stderr io.Writer) (gardenerrelease.JobArtifact, gardenerrelease.Policy, gardenerrelease.OperationSelector, bool) {
	artifact, err := readJobArtifact(files, artifactPath, gardenerrelease.JobValidateRequest)
	if err != nil || validateRuntimeArtifact(artifact, false) != nil {
		printError(stderr, gardenerrelease.NewCLIError("job_artifact_runtime_mismatch"))
		return gardenerrelease.JobArtifact{}, gardenerrelease.Policy{}, gardenerrelease.OperationSelector{}, false
	}
	policy, err := readPolicy(files, policyPath)
	if err != nil {
		printError(stderr, err)
		return gardenerrelease.JobArtifact{}, gardenerrelease.Policy{}, gardenerrelease.OperationSelector{}, false
	}
	var value struct {
		Request          gardenerrelease.DispatchRequest   `json:"request"`
		ValidatedRequest gardenerrelease.ValidatedRequest  `json:"validated_request"`
		Selector         gardenerrelease.OperationSelector `json:"selector"`
	}
	if gardenerrelease.DecodeJobPayload(artifact, &value) != nil || gardenerrelease.ValidateOperationSelector(value.ValidatedRequest, value.Selector) != nil || (next != "" && value.Selector.NextJob != next) || value.Selector.PolicyRevision != value.ValidatedRequest.PolicyRevision || value.Selector.PolicyRevision != policy.Revision || value.ValidatedRequest.RequestKey != artifact.RequestKey {
		printError(stderr, gardenerrelease.NewCLIError("selector_artifact_mismatch"))
		return gardenerrelease.JobArtifact{}, gardenerrelease.Policy{}, gardenerrelease.OperationSelector{}, false
	}
	return artifact, policy, value.Selector, true
}

func readPhaseInput(files gardenerrelease.FileSystem, artifactPath, policyPath string, job gardenerrelease.OrchestrationJob, stderr io.Writer) (gardenerrelease.JobArtifact, gardenerrelease.Policy, gardenerrelease.Record, string, bool) {
	artifact, err := readJobArtifact(files, artifactPath, job)
	if err != nil || validateRuntimePredecessor(artifact) != nil {
		printError(stderr, gardenerrelease.NewCLIError("job_artifact_runtime_mismatch"))
		return gardenerrelease.JobArtifact{}, gardenerrelease.Policy{}, gardenerrelease.Record{}, "", false
	}
	policy, err := readPolicy(files, policyPath)
	if err != nil {
		printError(stderr, err)
		return gardenerrelease.JobArtifact{}, gardenerrelease.Policy{}, gardenerrelease.Record{}, "", false
	}
	var record gardenerrelease.Record
	var head string
	var decodeErr error
	switch job {
	case gardenerrelease.JobPublishBranches, gardenerrelease.JobPublishTags:
		var value struct {
			Record      gardenerrelease.Record            `json:"record"`
			StateHead   string                            `json:"state_head"`
			Publication gardenerrelease.PublicationResult `json:"publication"`
		}
		decodeErr = gardenerrelease.DecodeJobPayload(artifact, &value)
		record, head = value.Record, value.StateHead
	case gardenerrelease.JobWaitBranchTests:
		var value struct {
			Record       gardenerrelease.Record               `json:"record"`
			StateHead    string                               `json:"state_head"`
			TestEvidence gardenerrelease.VerifiedTestEvidence `json:"test_evidence"`
		}
		decodeErr = gardenerrelease.DecodeJobPayload(artifact, &value)
		record, head = value.Record, value.StateHead
	case gardenerrelease.JobEnsurePreparePR:
		var value struct {
			Record    gardenerrelease.Record      `json:"record"`
			StateHead string                      `json:"state_head"`
			PRNumber  string                      `json:"pr_number"`
			Outcome   gardenerrelease.WorkOutcome `json:"outcome"`
		}
		decodeErr = gardenerrelease.DecodeJobPayload(artifact, &value)
		record, head = value.Record, value.StateHead
	case gardenerrelease.JobObserveImages:
		var value struct {
			Record        gardenerrelease.Record                   `json:"record"`
			StateHead     string                                   `json:"state_head"`
			ImageEvidence []gardenerrelease.ImagePromotionEvidence `json:"image_evidence"`
			Outcome       gardenerrelease.WorkOutcome              `json:"outcome"`
		}
		decodeErr = gardenerrelease.DecodeJobPayload(artifact, &value)
		record, head = value.Record, value.StateHead
	default:
		decodeErr = gardenerrelease.NewCLIError("job_artifact_phase_mismatch")
	}
	if decodeErr != nil || record.Reservation.RequestKey != artifact.RequestKey || record.SignedOutput == nil || !gardenerrelease.ValidGitObjectID(head) {
		printError(stderr, gardenerrelease.NewCLIError("job_artifact_state_mismatch"))
		return gardenerrelease.JobArtifact{}, gardenerrelease.Policy{}, gardenerrelease.Record{}, "", false
	}
	return artifact, policy, record, head, true
}

func validateRuntimeArtifact(artifact gardenerrelease.JobArtifact, requirePrevious bool) error {
	attempt, err := strconv.Atoi(os.Getenv("GITHUB_RUN_ATTEMPT"))
	if err != nil || (requirePrevious && artifact.PreviousSHA256 == "") || artifact.WorkflowRunID != os.Getenv("GITHUB_RUN_ID") || artifact.WorkflowAttempt != attempt || artifact.WorkflowSHA != os.Getenv("GITHUB_SHA") {
		return gardenerrelease.NewCLIError("job_artifact_runtime_mismatch")
	}
	return nil
}

func validateRuntimePredecessor(artifact gardenerrelease.JobArtifact) error {
	return validateRuntimeArtifact(artifact, true)
}

func writePhaseArtifact(stdout, stderr io.Writer, job gardenerrelease.OrchestrationJob, prior gardenerrelease.JobArtifact, payload any) int {
	raw, err := json.Marshal(payload)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	artifact, err := newRuntimeArtifact(job, prior.RequestKey, prior.ArtifactSHA256, raw)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	return writeJSON(stdout, stderr, artifact)
}

func argumentError(stderr io.Writer) int {
	printError(stderr, gardenerrelease.NewCLIError("invalid_arguments"))
	return 2
}

func readJobArtifact(files gardenerrelease.FileSystem, path string, job gardenerrelease.OrchestrationJob) (gardenerrelease.JobArtifact, error) {
	raw, err := gardenerrelease.ReadBoundedFile(files, path, gardenerrelease.MaxJobArtifactBytes)
	if err != nil {
		return gardenerrelease.JobArtifact{}, err
	}
	return gardenerrelease.DecodeJobArtifact(raw, job)
}

func readPolicy(files gardenerrelease.FileSystem, path string) (gardenerrelease.Policy, error) {
	raw, err := gardenerrelease.ReadBoundedFile(files, path, gardenerrelease.MaxPolicyBytes)
	if err != nil {
		return gardenerrelease.Policy{}, err
	}
	return gardenerrelease.DecodePolicy(raw)
}

func readDispatchAndPolicy(files gardenerrelease.FileSystem, dispatchPath, policyPath string) ([]byte, []byte, error) {
	dispatchRaw, err := gardenerrelease.ReadBoundedFile(files, dispatchPath, gardenerrelease.MaxContextBytes+1024)
	if err != nil {
		return nil, nil, err
	}
	policyRaw, err := gardenerrelease.ReadBoundedFile(files, policyPath, gardenerrelease.MaxPolicyBytes)
	return dispatchRaw, policyRaw, err
}

func newRuntimeArtifact(job gardenerrelease.OrchestrationJob, requestKey, previous string, payload json.RawMessage) (gardenerrelease.JobArtifact, error) {
	runID := os.Getenv("GITHUB_RUN_ID")
	attempt, err := strconv.Atoi(os.Getenv("GITHUB_RUN_ATTEMPT"))
	if err != nil {
		return gardenerrelease.JobArtifact{}, gardenerrelease.NewCLIError("invalid_runtime_identity")
	}
	return gardenerrelease.NewJobArtifact(job, requestKey, runID, attempt, os.Getenv("GITHUB_SHA"), previous, payload)
}

func writeJSON(stdout, stderr io.Writer, value any) int {
	data, err := gardenerrelease.MarshalInspection(value)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	_, _ = stdout.Write(data)
	_, _ = stdout.Write([]byte("\n"))
	return 0
}

func printError(stderr io.Writer, err error) {
	class := gardenerrelease.ClassOf(err)
	payload := map[string]string{"error": gardenerrelease.ErrorCode(err)}
	if class != "" {
		payload["class"] = string(class)
	}
	data, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		fmt.Fprintln(stderr, `{"error":"internal"}`)
		return
	}
	fmt.Fprintln(stderr, string(data))
}
