// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

// maxDiffExcerpt bounds the diff copied into the local diagnostic output. The
// full diff is kept in changes.diff and no diff content is sent to LLM Obs.
const maxDiffExcerpt = 8 << 10

// packagesGoPath is the registration file an auto-instrumentable integration has
// to be added to. Forgetting it is one of the failure modes the docs address.
const packagesGoPath = "instrumentation/packages.go"

// TaskRunner executes one task against one ref. It is safe for concurrent use.
type TaskRunner struct {
	// RepoDir is the control checkout. The agent never runs here and nothing in
	// this directory is modified.
	RepoDir string
	// MutationsDir holds the *.patch files referenced by dataset records.
	MutationsDir string
	// ResultsDir is where per-run artifacts are written.
	ResultsDir string
	Branch     string

	Runner Runner

	AgentTimeout      time.Duration
	ValidationTimeout time.Duration

	// KeepWorkspaces leaves the materialised trees on disk for inspection. They are
	// a full repo copy plus build output, so this is off by default.
	KeepWorkspaces bool

	seq atomic.Int64
}

// Run prepares a workspace at ref, applies the task mutation, runs the agent, and
// scores everything that requires the workspace to still exist.
//
// It returns an output for every attempt, including failed ones, because "the
// agent could not do this" is a result worth recording. A returned error means the
// harness itself could not set up the attempt, which is not a datapoint.
func (t *TaskRunner) Run(ctx context.Context, spec *TaskSpec, ref string, side Side) (*AgentRunOutput, error) {
	return t.RunCase(ctx, spec, ref, side, "default")
}

// RunCase executes one prompt variant for a task.
func (t *TaskRunner) RunCase(ctx context.Context, spec *TaskSpec, ref string, side Side, promptID string) (*AgentRunOutput, error) {
	for attempt := 1; attempt <= 2; attempt++ {
		out, err := t.runCaseAttempt(ctx, spec, ref, side, promptID)
		var infraErr *infrastructureError
		if !errors.As(err, &infraErr) {
			return out, err
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if attempt == 1 {
			progressf("%s/%s/%s: infrastructure failure, retrying once: %v", spec.TaskID, side, promptID, err)
			continue
		}
		progressf("%s/%s/%s: infrastructure failure after retry, excluding record from scores: %v", spec.TaskID, side, promptID, err)
		return out, nil
	}
	panic("unreachable")
}

type infrastructureError struct {
	cause error
}

func (e *infrastructureError) Error() string { return e.cause.Error() }
func (e *infrastructureError) Unwrap() error { return e.cause }

func (t *TaskRunner) runCaseAttempt(ctx context.Context, spec *TaskSpec, ref string, side Side, promptID string) (*AgentRunOutput, error) {
	attempt := t.seq.Add(1)
	artifactDir := filepath.Join(t.ResultsDir, string(side), spec.TaskID, strconv.FormatInt(attempt, 10))
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, err
	}
	workspace := filepath.Join(artifactDir, "workspace")
	if !t.KeepWorkspaces {
		defer os.RemoveAll(workspace)
	}

	// session labels every line of this attempt, so interleaved output from
	// concurrent attempts stays attributable.
	session := fmt.Sprintf("%s/%s/%s#%d", spec.TaskID, side, promptID, attempt)
	caseStarted := time.Now()

	prepStarted := time.Now()
	progressf("%s: preparing workspace at %s", session, ref)
	if err := MaterializeTree(ctx, t.RepoDir, ref, workspace); err != nil {
		return nil, fmt.Errorf("materialise %s at %s: %w", spec.TaskID, ref, err)
	}
	if err := ApplyMutation(ctx, workspace, t.MutationsDir, spec.Mutation); err != nil {
		return nil, fmt.Errorf("mutate %s at %s: %w", spec.TaskID, ref, err)
	}
	progressf("%s: workspace ready in %s (mutation %s)", session, fmtDuration(time.Since(prepStarted)), spec.Mutation)
	// Fold the mutation into the base commit so the diff collected later describes
	// only what the agent did.
	if err := CommitAll(ctx, workspace, "apply task mutation"); err != nil {
		return nil, err
	}

	out := &AgentRunOutput{
		TaskID:      spec.TaskID,
		PromptID:    promptID,
		Agent:       t.Runner.Name(),
		Model:       t.Runner.Model(),
		Ref:         ref,
		Side:        side,
		Branch:      t.Branch,
		Status:      RunStatusCompleted,
		ArtifactDir: artifactDir,
		Checks:      map[string]float64{},
		Diagnostics: map[string]bool{},
		Validations: map[string]bool{},
	}

	agentCtx := ctx
	if t.AgentTimeout > 0 {
		var cancel context.CancelFunc
		agentCtx, cancel = context.WithTimeout(ctx, t.AgentTimeout)
		defer cancel()
	}

	budget := "no timeout"
	if t.AgentTimeout > 0 {
		budget = "timeout " + fmtDuration(t.AgentTimeout)
	}
	progressf("%s: starting %s (%s) to run the task, %s", session, t.Runner.Name(), t.Runner.Model(), budget)

	started := time.Now()
	run, err := t.Runner.Run(agentCtx, workspace, spec.Prompt, artifactDir)
	if err != nil {
		progressf("%s: agent session failed after %s: %v", session, fmtDuration(time.Since(started)), err)
		out.Status = RunStatusInfrastructureFailure
		out.InfrastructureError = err.Error()
		if writeErr := writeJSON(filepath.Join(artifactDir, "output.json"), out); writeErr != nil {
			return nil, writeErr
		}
		return out, &infrastructureError{cause: fmt.Errorf("run agent for %s at %s: %w", spec.TaskID, ref, err)}
	}
	agentElapsed := time.Since(started)
	progressf("%s: agent finished in %s, %s, %s", session, fmtDuration(agentElapsed), outcome(run), usageSummary(run))
	out.DurationMillis = agentElapsed.Milliseconds()
	if run.DurationMillis > 0 {
		out.DurationMillis = run.DurationMillis
	}
	out.FinalMessage = headString(run.FinalMessage, 4<<10)
	out.ExitCode = run.ExitCode
	out.Turns = run.Turns
	out.PermissionDenials = run.PermissionDenials
	out.CostUSD = run.CostUSD
	out.InputTokens = run.InputTokens
	out.OutputTokens = run.OutputTokens
	out.TokenCount = run.TokenCount
	out.CachedInputTokens = run.CachedInputTokens
	out.CacheWriteInputTokens = run.CacheWriteInputTokens
	out.ReasoningOutputTokens = run.ReasoningOutputTokens
	out.ToolCalls = len(run.ToolCalls)
	if run.IsError {
		out.Error = "agent reported an error result"
	}
	if reason := infrastructureFailure(run); reason != "" {
		out.Status = RunStatusInfrastructureFailure
		out.InfrastructureError = reason
		out.Diagnostics[CheckAgentExitedOK] = false
		out.Diagnostics[CheckNoPermissionDenials] = run.PermissionDenials == 0
		if err := writeJSON(filepath.Join(artifactDir, "output.json"), out); err != nil {
			return nil, err
		}
		return out, &infrastructureError{cause: errors.New(reason)}
	}

	diff, changed, err := Diff(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("diff %s at %s: %w", spec.TaskID, ref, err)
	}
	out.ChangedFiles = changed
	out.DiffLineCount = DiffLineCount(diff)
	out.DiffExcerpt = headString(diff, maxDiffExcerpt)
	if err := os.WriteFile(filepath.Join(artifactDir, "changes.diff"), []byte(diff), 0o644); err != nil {
		return nil, err
	}

	progressf("%s: agent changed %d files, %d diff lines", session, len(changed), out.DiffLineCount)

	if len(spec.ValidationCommands) > 0 {
		valStarted := time.Now()
		progressf("%s: running %d validation commands", session, len(spec.ValidationCommands))
		out.CommandResults = RunValidation(ctx, workspace, spec.ValidationCommands, t.ValidationTimeout)
		var valPassed int
		for _, res := range out.CommandResults {
			if res.Passed() {
				valPassed++
			}
		}
		progressf("%s: validation %d/%d passed in %s", session, valPassed, len(out.CommandResults), fmtDuration(time.Since(valStarted)))
		for _, res := range out.CommandResults {
			if !res.Passed() {
				progressf("%s:   FAILED validation %s", session, res.Label)
			}
		}
	}
	out.DocsRead = docsRead(spec, run, workspace)

	t.computeChecks(out, spec, run, workspace, changed)
	out.ChecksPassed, out.ChecksTotal, out.ChecksScore = weightedTally(out.Checks, spec.CheckWeights)
	out.ValidationPassed, out.ValidationTotal, out.ValidationScore = tally(out.Validations)
	progressf("%s: done in %s, checks %d/%d passed (%.0f%%), validations %d/%d passed (%.0f%%), artifacts in %s",
		session, fmtDuration(time.Since(caseStarted)), out.ChecksPassed, out.ChecksTotal, 100*out.ChecksScore,
		out.ValidationPassed, out.ValidationTotal, 100*out.ValidationScore, artifactDir)

	localOutput := struct {
		Output         *AgentRunOutput `json:"output"`
		FinalMessage   string          `json:"final_message,omitempty"`
		DiffExcerpt    string          `json:"diff_excerpt,omitempty"`
		CommandResults []CmdResult     `json:"command_results,omitempty"`
	}{out, out.FinalMessage, out.DiffExcerpt, out.CommandResults}
	if err := writeJSON(filepath.Join(artifactDir, "output.json"), localOutput); err != nil {
		return nil, err
	}
	return out, nil
}

func infrastructureFailure(run *RunResult) string {
	switch {
	case run.TimedOut:
		return "agent container timed out"
	case run.ExitCode == 137:
		return "agent container was killed with exit code 137"
	case run.ExitCode != 0 && run.Turns == 0 && run.TokenCount == 0:
		return fmt.Sprintf("agent CLI exited with code %d before starting a model turn", run.ExitCode)
	default:
		return ""
	}
}

// computeChecks records every criterion that applies to this task. Criteria that
// do not apply are left absent rather than set to false, so a task that was never
// about (say) Orchestrion is not counted as having failed at it.
//
// Everything needing the workspace is decided here, while it still exists;
// evaluators run later and only read this map.
func (t *TaskRunner) computeChecks(out *AgentRunOutput, spec *TaskSpec, run *RunResult, workspace string, changed []string) {
	out.Diagnostics[CheckAgentExitedOK] = run.ExitCode == 0 && !run.IsError
	out.Diagnostics[CheckNoPermissionDenials] = run.PermissionDenials == 0
	out.Checks[CheckDiffNotEmpty] = boolScore(out.DiffLineCount > 0)

	if len(spec.ValidationCommands) > 0 {
		for _, res := range out.CommandResults {
			out.Validations[res.Label] = res.Passed()
		}
	}
	if len(spec.ExpectedChangedPaths) > 0 {
		out.Checks[CheckExpectedPathsTouched] = expectedPathsScore(changed, spec.ExpectedChangedPaths)
	}
	if len(spec.ForbiddenPaths) > 0 {
		out.Checks[CheckForbiddenPathsUntouched] = forbiddenPathsScore(changed, spec.ForbiddenPaths)
	}
	if spec.MaxDiffLines > 0 {
		out.Checks[CheckDiffWithinLimit] = boolScore(out.DiffLineCount <= spec.MaxDiffLines)
	}
	if spec.RegistrationImport != "" {
		out.Checks[CheckRegisteredInPackagesGo] = boolScore(registeredInPackagesGo(workspace, changed, spec.RegistrationImport))
	}
	if spec.OrchestrionYAML != "" {
		out.Checks[CheckOrchestrionAspect] = boolScore(orchestrionAspectPresent(workspace, spec.OrchestrionYAML))
		err := ValidateOrchestrionYAML(context.Background(), workspace, spec.OrchestrionYAML)
		out.Checks[CheckOrchestrionSchemaValid] = boolScore(err == nil)
		if err != nil {
			out.SchemaErrors = append(out.SchemaErrors, err.Error())
		}
	}
	if len(spec.DocsExpectedRead) > 0 {
		out.Diagnostics[CheckDocsOpened] = len(out.DocsRead) > 0
	}
	if len(spec.UpstreamMarkers) > 0 {
		out.Diagnostics[CheckNoUpstreamFetch] = !upstreamConsulted(spec, run)
	}
	if len(spec.RequiredPaths) > 0 {
		out.Checks[CheckRequiredPathsPresent] = boolScore(allPathsExist(workspace, spec.RequiredPaths))
	}
	for _, sc := range spec.SourceChecks {
		out.Checks[sc.Label] = boolScore(sourceCheckSatisfied(workspace, sc))
	}
}

func boolScore(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func expectedPathsScore(changed, expected []string) float64 {
	if len(expected) == 0 {
		return 1
	}
	var touched int
	for _, path := range expected {
		if AnyPathMatches(changed, []string{path}) {
			touched++
		}
	}
	return float64(touched) / float64(len(expected))
}

func forbiddenPathsScore(changed, forbidden []string) float64 {
	var touched int
	for _, path := range changed {
		if PathMatches(path, forbidden) {
			touched++
		}
	}
	return 1 / float64(1+touched)
}

// allPathsExist reports whether every required artifact is there. A directory
// counts only if it is non-empty, since an integration test package that exists
// but holds no files is the same failure as not writing one.
func allPathsExist(workspace string, paths []string) bool {
	for _, rel := range paths {
		target, err := safeJoin(workspace, rel)
		if err != nil {
			return false
		}
		info, err := os.Stat(target)
		if err != nil {
			return false
		}
		if !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(target)
		if err != nil || len(entries) == 0 {
			return false
		}
	}
	return true
}

// sourceCheckSatisfied evaluates one convention assertion against the finished
// tree. A glob selecting no file fails either direction, so an agent cannot
// satisfy an Absent check by deleting the file the check is about.
func sourceCheckSatisfied(workspace string, sc SourceCheck) bool {
	re, err := regexp.Compile(sc.Pattern)
	if err != nil {
		return false
	}
	var matchedAny, sawFile bool
	for _, glob := range sc.Paths {
		full, err := safeJoin(workspace, glob)
		if err != nil {
			return false
		}
		hits, err := filepath.Glob(full)
		if err != nil {
			return false
		}
		for _, hit := range hits {
			info, err := os.Stat(hit)
			if err != nil || info.IsDir() {
				continue
			}
			body, err := os.ReadFile(hit)
			if err != nil {
				continue
			}
			sawFile = true
			if re.Match(body) {
				matchedAny = true
			}
		}
	}
	if !sawFile {
		return false
	}
	if sc.Absent {
		return !matchedAny
	}
	return matchedAny
}

// registeredInPackagesGo requires both that the registration file changed and
// that it now mentions the package. Either alone is not enough: an unrelated edit
// to packages.go should not count, and a mention that was already there proves
// nothing.
func registeredInPackagesGo(workspace string, changed []string, importPath string) bool {
	if !AnyPathMatches(changed, []string{packagesGoPath}) {
		return false
	}
	body, err := os.ReadFile(filepath.Join(workspace, packagesGoPath))
	if err != nil {
		return false
	}
	return strings.Contains(string(body), importPath)
}

// orchestrionAspectPresent requires the aspect file to exist and to be valid YAML.
// A malformed aspect file is the interesting failure: the build succeeds and
// auto-instrumentation silently does nothing.
func orchestrionAspectPresent(workspace, rel string) bool {
	body, err := os.ReadFile(filepath.Join(workspace, rel))
	if err != nil || len(strings.TrimSpace(string(body))) == 0 {
		return false
	}
	var doc any
	return yaml.Unmarshal(body, &doc) == nil
}

// docsRead reports which of the expected docs the agent actually consulted.
// A doc counts if it was read or searched directly, or named in a shell command,
// since `grep -rn ... contrib/ORCHESTRION.md` is consulting it just as much as Read is.
func docsRead(spec *TaskSpec, run *RunResult, workspace string) []string {
	if len(spec.DocsExpectedRead) == 0 {
		return nil
	}
	haystack := make([]string, 0, len(run.ToolCalls)*2)
	for _, p := range run.ReadPaths() {
		haystack = append(haystack, strings.TrimPrefix(strings.TrimPrefix(p, workspace), "/"))
	}
	haystack = append(haystack, run.ShellCommands()...)

	var found []string
	for _, doc := range spec.DocsExpectedRead {
		base := filepath.Base(doc)
		for _, h := range haystack {
			if strings.Contains(h, doc) || strings.Contains(h, base) {
				found = append(found, doc)
				break
			}
		}
	}
	return found
}

// upstreamConsulted reports whether the agent went looking for the reference
// implementation. The workspace has no git history to read it from, but the
// network is still reachable because the agent needs the model API, so this is
// detection rather than prevention.
//
// Only fetch targets are examined. Shell commands were included once and made
// this useless: a marker specific enough to identify the upstream source is
// usually also the package's own import path or directory, which appears in
// almost every legitimate command the agent runs.
func upstreamConsulted(spec *TaskSpec, run *RunResult) bool {
	probes := run.FetchTargets()
	for _, marker := range spec.UpstreamMarkers {
		for _, p := range probes {
			if strings.Contains(p, marker) {
				return true
			}
		}
	}
	return false
}

func writeJSON(path string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}
