// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
)

// Side identifies which of the two compared git revisions a run belongs to.
type Side string

const (
	SideBaseline  Side = "baseline"
	SideCandidate Side = "candidate"
)

// MutationKind selects how a task removes the work the agent has to redo.
type MutationKind string

const (
	// MutationDeletePaths removes whole files or directories.
	MutationDeletePaths MutationKind = "delete_paths"
	// MutationApplyPatch applies a patch that strips the feature. Use it when the
	// thing to remove is smaller than a file, e.g. one registration entry.
	MutationApplyPatch MutationKind = "apply_patch"
	// MutationNone leaves the tree alone. It is for tasks that ask for something
	// the repository does not have yet, where absence is already the starting
	// state and there is nothing to remove.
	MutationNone MutationKind = "none"
)

// Mutation describes the tree change applied before the agent runs. It must
// apply identically to both compared refs, otherwise the record's two sides
// are not measuring the same task. See VerifyMutation.
type Mutation struct {
	Kind MutationKind `json:"kind"`
	// Paths are repo-relative, for MutationDeletePaths.
	Paths []string `json:"paths,omitempty"`
	// AllowMissing makes deletion a no-op when a path has not landed yet.
	AllowMissing bool `json:"allow_missing,omitempty"`
	// Patch is a file name resolved against the mutations directory, for
	// MutationApplyPatch. Patches live in git rather than in dataset records
	// because they are code and need reviewing.
	Patch string `json:"patch,omitempty"`
	// AssertAbsent are repo-relative paths that must not exist, for
	// MutationNone. A task asking for something new goes stale the moment that
	// thing lands, and unlike the other kinds there is no apply step to notice.
	AssertAbsent []string `json:"assert_absent,omitempty"`
}

// SourceCheck is a named assertion over the workspace once the agent has
// finished: a regexp that must, or must not, match the contents of the files a
// glob selects.
//
// Most review findings on a real integration PR have this shape, so keeping them
// as data means the next finding is a dataset entry rather than a harness
// change. A glob that selects no file fails the check in either direction, so
// deleting the file under scrutiny cannot pass an Absent check.
type SourceCheck struct {
	// Label names the criterion in LLM Obs as check_<label>. It must stay
	// stable across records and across both sides.
	Label string `json:"label"`
	// Paths are repo-relative globs with filepath.Match semantics.
	Paths []string `json:"paths"`
	// Pattern is a Go regexp tested against each selected file's contents.
	Pattern string `json:"pattern"`
	// Absent inverts the check: no selected file may match.
	Absent bool `json:"absent,omitempty"`
}

// ValidationCommand is a shell command run after the agent finishes. Label is
// what the criterion is called in LLM Obs, so it must stay stable across
// records and across the two sides; the command itself may differ per task.
type ValidationCommand struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

// TaskSpec is the hidden setup and scoring definition for one eval task.
type TaskSpec struct {
	TaskID         string `json:"task_id"`
	ExperimentName string `json:"experiment_name,omitempty"`
	Prompt         string `json:"prompt"`

	Mutation Mutation `json:"mutation"`

	ValidationCommands   []ValidationCommand `json:"validation_commands,omitempty"`
	ExpectedChangedPaths []string            `json:"expected_changed_paths,omitempty"`
	ForbiddenPaths       []string            `json:"forbidden_paths,omitempty"`
	MaxDiffLines         int                 `json:"max_diff_lines,omitempty"`
	DocsExpectedRead     []string            `json:"docs_expected_read,omitempty"`
	// CheckWeights controls each check's contribution to checks_score. Checks
	// without an entry have weight 1.
	CheckWeights map[string]float64 `json:"check_weights,omitempty"`

	// SourceChecks are convention assertions over the finished tree, one
	// criterion each. See SourceCheck.
	SourceChecks []SourceCheck `json:"source_checks,omitempty"`
	// RequiredPaths must all exist after the run, enabling the
	// required_paths_present criterion. Use it for whole artifacts an
	// integration owes, such as its package under internal/orchestrion/_integration.
	RequiredPaths []string `json:"required_paths,omitempty"`

	// RegistrationImport enables the registered_in_packages_go criterion:
	// instrumentation/packages.go must be modified and mention this import path.
	RegistrationImport string `json:"registration_import,omitempty"`
	// OrchestrionYAML enables the orchestrion_aspect_present and
	// orchestrion_aspect_schema_valid criteria. It is a repo-relative path that
	// must exist, parse as YAML, and conform to Orchestrion's own JSON schema
	// after the run. The two are separate because a file that exists but does
	// not conform is a different failure from no file at all, and the schema
	// one is silent at build time.
	OrchestrionYAML string `json:"orchestrion_yaml,omitempty"`
	// UpstreamMarkers are strings which, appearing in a fetch or a shell command,
	// mean the agent went looking for the reference implementation instead of
	// writing it. Contamination, not a correct solution.
	UpstreamMarkers []string `json:"upstream_markers,omitempty"`
}

// TaskInput is the user-visible input for one dataset record. Harness setup and
// scoring rules stay in TaskSpec rather than being uploaded as input noise.
type TaskInput struct {
	TaskID   string `json:"task_id"`
	PromptID string `json:"prompt_id"`
	Prompt   string `json:"prompt"`
}

// DecodeTaskInput converts a pulled dataset record into its minimal input.
func DecodeTaskInput(rec dataset.Record) (*TaskInput, error) {
	raw, err := json.Marshal(rec.Input)
	if err != nil {
		return nil, fmt.Errorf("marshal record input: %w", err)
	}
	var input TaskInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("decode task input: %w", err)
	}
	if input.TaskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if input.PromptID == "" {
		return nil, fmt.Errorf("%s: prompt_id is required", input.TaskID)
	}
	if input.Prompt == "" {
		return nil, fmt.Errorf("%s: prompt is required", input.TaskID)
	}
	return &input, nil
}

// Validate rejects specs that would fail late, mid-run, rather than at load time.
func (s *TaskSpec) Validate() error {
	if s.TaskID == "" {
		return fmt.Errorf("task_id is required")
	}
	if s.Prompt == "" {
		return fmt.Errorf("%s: prompt is required", s.TaskID)
	}
	switch s.Mutation.Kind {
	case MutationDeletePaths:
		if len(s.Mutation.Paths) == 0 {
			return fmt.Errorf("%s: mutation %q requires paths", s.TaskID, s.Mutation.Kind)
		}
		if s.Mutation.Patch != "" {
			return fmt.Errorf("%s: mutation %q must not set patch", s.TaskID, s.Mutation.Kind)
		}
	case MutationApplyPatch:
		if s.Mutation.Patch == "" {
			return fmt.Errorf("%s: mutation %q requires patch", s.TaskID, s.Mutation.Kind)
		}
		if len(s.Mutation.Paths) != 0 {
			return fmt.Errorf("%s: mutation %q must not set paths", s.TaskID, s.Mutation.Kind)
		}
		if s.Mutation.AllowMissing {
			return fmt.Errorf("%s: allow_missing only applies to mutation %q", s.TaskID, MutationDeletePaths)
		}
	case MutationNone:
		if len(s.Mutation.Paths) != 0 || s.Mutation.Patch != "" || s.Mutation.AllowMissing {
			return fmt.Errorf("%s: mutation %q must not set paths, patch or allow_missing", s.TaskID, s.Mutation.Kind)
		}
		if len(s.Mutation.AssertAbsent) == 0 {
			return fmt.Errorf("%s: mutation %q requires assert_absent, otherwise the record cannot go stale detectably", s.TaskID, s.Mutation.Kind)
		}
	default:
		return fmt.Errorf("%s: unknown mutation kind %q", s.TaskID, s.Mutation.Kind)
	}
	if s.Mutation.Kind != MutationNone && len(s.Mutation.AssertAbsent) != 0 {
		return fmt.Errorf("%s: assert_absent only applies to mutation %q", s.TaskID, MutationNone)
	}
	seen := make(map[string]struct{}, len(s.ValidationCommands))
	for _, vc := range s.ValidationCommands {
		if vc.Label == "" || vc.Command == "" {
			return fmt.Errorf("%s: validation command needs both label and command", s.TaskID)
		}
		if _, dup := seen[vc.Label]; dup {
			return fmt.Errorf("%s: duplicate validation label %q", s.TaskID, vc.Label)
		}
		seen[vc.Label] = struct{}{}
	}
	// Compile patterns here so a bad regexp fails at seed time rather than
	// after the agent has already spent an hour on the task.
	seenChecks := make(map[string]struct{}, len(s.SourceChecks))
	for _, sc := range s.SourceChecks {
		if sc.Label == "" || sc.Pattern == "" || len(sc.Paths) == 0 {
			return fmt.Errorf("%s: source check needs label, pattern and paths", s.TaskID)
		}
		if _, dup := seenChecks[sc.Label]; dup {
			return fmt.Errorf("%s: duplicate source check label %q", s.TaskID, sc.Label)
		}
		seenChecks[sc.Label] = struct{}{}
		if _, err := regexp.Compile(sc.Pattern); err != nil {
			return fmt.Errorf("%s: source check %q: %w", s.TaskID, sc.Label, err)
		}
	}
	knownChecks := expectedChecks(s)
	for name, weight := range s.CheckWeights {
		if _, ok := knownChecks[name]; !ok {
			return fmt.Errorf("%s: weight declared for unknown check %q", s.TaskID, name)
		}
		if weight <= 0 {
			return fmt.Errorf("%s: check %q weight must be greater than zero", s.TaskID, name)
		}
	}
	return nil
}

// CmdResult is the outcome of one validation command.
type CmdResult struct {
	Label    string `json:"label"`
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	// TimedOut distinguishes a command we killed from one that genuinely failed.
	TimedOut bool `json:"timed_out"`
}

// Passed reports whether the command succeeded.
func (c CmdResult) Passed() bool { return c.ExitCode == 0 && !c.TimedOut }

// AgentRunOutput is what the experiment task returns, and therefore what every
// evaluator scores. It is serialised onto the experiment span, so bulky
// artifacts (full transcript, full diff) stay on disk under ArtifactDir and
// only excerpts and paths live here.
type AgentRunOutput struct {
	TaskID   string `json:"task_id"`
	PromptID string `json:"prompt_id"`
	Agent    string `json:"agent"`
	Model    string `json:"model,omitempty"`
	Ref      string `json:"ref"`
	Side     Side   `json:"side"`
	Branch   string `json:"branch"`
	Status   string `json:"status"`

	FinalMessage string `json:"-"`
	ArtifactDir  string `json:"-"`

	ExitCode            int    `json:"exit_code"`
	Error               string `json:"error,omitempty"`
	InfrastructureError string `json:"infrastructure_error,omitempty"`

	ChangedFiles  []string `json:"changed_paths"`
	DiffExcerpt   string   `json:"-"`
	DiffLineCount int      `json:"diff_line_count"`

	CommandResults   []CmdResult     `json:"-"`
	Validations      map[string]bool `json:"validations,omitempty"`
	ValidationTotal  int             `json:"validation_total"`
	ValidationPassed int             `json:"validation_passed"`
	ValidationScore  float64         `json:"validation_score"`

	DocsRead          []string `json:"docs_read,omitempty"`
	ToolCalls         int      `json:"tool_calls"`
	Turns             int      `json:"turns"`
	PermissionDenials int      `json:"permission_denials"`

	// Checks holds only the deterministic criteria that apply to this task.
	Checks       map[string]float64 `json:"checks"`
	Diagnostics  map[string]bool    `json:"diagnostics"`
	ChecksTotal  int                `json:"checks_total"`
	ChecksPassed int                `json:"checks_passed"`
	ChecksScore  float64            `json:"checks_score"`

	// SchemaErrors records why a schema validation failed, so a red column can
	// be explained without reopening the workspace, which is deleted by then.
	SchemaErrors []string `json:"schema_errors,omitempty"`

	DurationMillis        int64   `json:"duration_millis"`
	CostUSD               float64 `json:"estimated_cost,omitempty"`
	InputTokens           int64   `json:"input_tokens,omitempty"`
	OutputTokens          int64   `json:"output_tokens,omitempty"`
	TokenCount            int64   `json:"token_count,omitempty"`
	CachedInputTokens     int64   `json:"-"`
	CacheWriteInputTokens int64   `json:"-"`
	ReasoningOutputTokens int64   `json:"-"`
}

const (
	RunStatusCompleted             = "completed"
	RunStatusInfrastructureFailure = "infrastructure_failure"
)

// Criterion names. These become LLM Obs evaluator names, so they are stable
// identifiers and must not be derived from per-record data.
const (
	CheckAgentExitedOK           = "agent_exited_ok"
	CheckDiffNotEmpty            = "diff_not_empty"
	CheckRegisteredInPackagesGo  = "registered_in_packages_go"
	CheckOrchestrionAspect       = "orchestrion_aspect_present"
	CheckOrchestrionSchemaValid  = "orchestrion_aspect_schema_valid"
	CheckExpectedPathsTouched    = "expected_paths"
	CheckForbiddenPathsUntouched = "forbidden_paths"
	CheckDiffWithinLimit         = "diff_within_limit"
	CheckDocsOpened              = "docs_opened"
	CheckNoUpstreamFetch         = "no_upstream_fetch"
	CheckNoPermissionDenials     = "no_permission_denials"
	CheckRequiredPathsPresent    = "required_paths_present"
)

// Result returns the named validation result.
func (o *AgentRunOutput) Result(label string) (CmdResult, bool) {
	for _, r := range o.CommandResults {
		if r.Label == label {
			return r, true
		}
	}
	return CmdResult{}, false
}
