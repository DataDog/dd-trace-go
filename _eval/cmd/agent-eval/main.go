// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Command agent-eval measures whether a change to this repository makes coding
// agents better at real repository tasks.
//
// It compares two git revisions, normally main against a branch head, by running
// the same agent tasks against each and scoring both with the same deterministic
// criteria. Results are reported as LLM Obs experiments. It is a local developer
// tool: there is no CI entry point.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/DataDog/dd-trace-go/_eval/internal/agenteval"
	"github.com/DataDog/dd-trace-go/_eval/suites"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
)

const usage = `agent-eval measures whether a repo change improves coding-agent behaviour.

Usage:
  agent-eval suites             list the registered task suites
  agent-eval seed     [flags]   push a suite to LLM Obs
  agent-eval verify   [flags]   check every task mutation still applies to a ref
  agent-eval compare  [flags]   run task experiments against two refs

Every command except "suites" takes -suite; see "agent-eval suites" for the names.
Run "agent-eval <command> -h" for the flags of a command.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "suites":
		err = runSuites()
	case "seed":
		err = runSeed(ctx, os.Args[2:])
	case "verify":
		err = runVerify(ctx, os.Args[2:])
	case "compare":
		err = runCompare(ctx, os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-eval: %v\n", err)
		os.Exit(1)
	}
}

// defaultSuite is what runs when -suite is not given. Any registered suite is a
// valid choice; this one is simply the oldest.
const defaultSuite = "integration-authoring"

const (
	defaultClaudeModel = "claude-sonnet-5"
	defaultCodexModel  = "gpt-5.6-terra"
)

// runSuites is the discovery command. Suites register themselves from init, so
// this is the only way to find out what is available without reading the source.
func runSuites() error {
	for _, s := range suites.All() {
		fmt.Printf("%s\n  dataset: %s\n  tasks:   %d\n  docs:    %s\n  %s\n\n",
			s.Name, s.Dataset, len(s.Tasks), strings.Join(s.Docs, ", "), s.Description)
	}
	return nil
}

// commonFlags are shared by every subcommand that talks to LLM Obs.
type commonFlags struct {
	repoDir     string
	projectName string
	suiteName   string
	datasetName string
	mlApp       string

	suite *suites.Suite
}

func (c *commonFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.repoDir, "repo", "..", "path to the dd-trace-go checkout to evaluate")
	fs.StringVar(&c.projectName, "project", "dd-trace-go-agent-docs", "LLM Obs project name")
	fs.StringVar(&c.suiteName, "suite", defaultSuite, "task suite to use (see: agent-eval suites)")
	fs.StringVar(&c.datasetName, "dataset", "", "LLM Obs dataset prefix (defaults to the suite's own)")
	fs.StringVar(&c.mlApp, "ml-app", "dd-trace-go-agent-eval", "LLM Obs ML app name")
}

// allSuites is the -suite value that means every registered suite. Only verify
// accepts it: seeding or comparing writes one dataset, so it must name one suite.
const allSuites = "all"

func (c *commonFlags) resolveRepo() error {
	abs, err := filepath.Abs(c.repoDir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return fmt.Errorf("%s does not look like a git checkout: %w", abs, err)
	}
	c.repoDir = abs
	return nil
}

func (c *commonFlags) resolve() error {
	if err := c.resolveRepo(); err != nil {
		return err
	}
	suite, err := suites.Lookup(c.suiteName)
	if err != nil {
		return err
	}
	c.suite = suite
	// The prefix produces one dataset per task. Overriding it is useful for
	// scratch datasets while developing a suite.
	if c.datasetName == "" {
		c.datasetName = suite.Dataset
	}
	return nil
}

// selectedSuites expands -suite for the commands that can operate on more than
// one.
func (c *commonFlags) selectedSuites() ([]*suites.Suite, error) {
	if c.suiteName == allSuites {
		return suites.All(), nil
	}
	suite, err := suites.Lookup(c.suiteName)
	if err != nil {
		return nil, err
	}
	return []*suites.Suite{suite}, nil
}

// mutationsDir is per suite, so two suites cannot collide on a patch file name.
func (c *commonFlags) mutationsDir() string {
	return filepath.Join(mustSelfDir(), "mutations", c.suiteName)
}

// startLLMObs initialises the tracer. Datasets and experiments need a project name
// and an ML app. Agentless mode additionally needs DD_APP_KEY, which is the usual
// case for a local run with no Datadog agent running.
func startLLMObs(c *commonFlags) (func(), error) {
	if err := tracer.Start(
		tracer.WithLLMObsEnabled(true),
		tracer.WithLLMObsMLApp(c.mlApp),
		tracer.WithLLMObsProjectName(c.projectName),
		tracer.WithLogStartup(false),
		tracer.WithLogger(quietTracerLogger{}),
	); err != nil {
		return nil, fmt.Errorf("start LLM Obs (need DD_API_KEY, and DD_APP_KEY in agentless mode): %w", err)
	}
	return tracer.Stop, nil
}

// agentlessNoise matches the two tracer errors that a run without a local
// Datadog agent always produces. LLM Obs submits agentlessly here, so neither
// affects the results, and "lost N traces" repeats on every flush, which buries
// the progress output.
//
// The patterns match the message text rather than the underlying network error,
// so a genuine transport failure on a path that matters still prints.
var agentlessNoise = regexp.MustCompile(`ERROR: (loading features:|lost \d+ traces?:)`)

// quietTracerLogger drops the agentless noise and passes every other tracer
// message through, so a real failure such as a rejected API key still surfaces.
type quietTracerLogger struct{}

func (quietTracerLogger) Log(msg string) {
	if agentlessNoise.MatchString(msg) {
		return
	}
	log.Print(msg)
}

func runSeed(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	var c commonFlags
	c.bind(fs)
	dryRun := fs.Bool("dry-run", false, "validate the records and print them without pushing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	suite, err := suites.Lookup(c.suiteName)
	if err != nil {
		return err
	}
	if *dryRun {
		datasets := make(map[string][]dataset.Record, len(suite.Tasks))
		var total int
		prefix := c.datasetName
		if prefix == "" {
			prefix = suite.Dataset
		}
		for _, task := range suite.Tasks {
			records, err := task.Records(suite.Name)
			if err != nil {
				return err
			}
			datasets[suites.TaskDatasetName(prefix, task.Spec.TaskID)] = records
			total += len(records)
		}
		body, err := json.MarshalIndent(datasets, "", "  ")
		if err != nil {
			return err
		}
		fmt.Printf("%s\n%d records validated across %d task datasets for suite %s\n", body, total, len(datasets), suite.Name)
		return nil
	}

	if err := c.resolve(); err != nil {
		return err
	}
	stop, err := startLLMObs(&c)
	if err != nil {
		return err
	}
	defer stop()

	_, err = syncTaskDatasets(ctx, &c, suite.Tasks)
	return err
}

func syncTaskDatasets(ctx context.Context, c *commonFlags, tasks []agenteval.Task) (map[string]agenteval.DatasetPin, error) {
	pins := make(map[string]agenteval.DatasetPin, len(tasks))
	for _, task := range tasks {
		taskID := task.Spec.TaskID
		records, err := task.Records(c.suite.Name)
		if err != nil {
			return nil, err
		}
		name := suites.TaskDatasetName(c.datasetName, taskID)
		description := c.suite.Description + " Task: " + taskID + "."
		ds, created, err := syncDataset(ctx, name, c.projectName, description, records)
		if err != nil {
			return nil, fmt.Errorf("sync task %s: %w", taskID, err)
		}
		action := "updated"
		if created {
			action = "created"
		}
		fmt.Printf("%s dataset %s version %d: %s\n", action, ds.Name(), ds.Version(), ds.URL())
		pins[taskID] = agenteval.DatasetPin{Name: ds.Name(), Version: ds.Version()}
	}
	return pins, nil
}

func syncDataset(ctx context.Context, name, project, description string, records []dataset.Record) (*dataset.Dataset, bool, error) {
	desired := make(map[string]dataset.Record, len(records))
	order := make([]string, 0, len(records))
	for _, rec := range records {
		key, err := datasetRecordKey(rec)
		if err != nil {
			return nil, false, err
		}
		desired[key] = rec
		order = append(order, key)
	}

	ds, err := dataset.Pull(ctx, name, dataset.WithPullProjectName(project))
	if err != nil {
		ds, err = dataset.Create(ctx, name, records,
			dataset.WithProjectName(project),
			dataset.WithDescription(description),
		)
		if err != nil {
			return nil, false, fmt.Errorf("create dataset: %w", err)
		}
		ds, err = waitForDatasetRecords(ctx, name, project, desired)
		if err != nil {
			return nil, false, fmt.Errorf("validate created dataset: %w", err)
		}
		return ds, true, nil
	}

	seen := make(map[string]struct{}, len(records))
	var stale []int
	for i := 0; i < ds.Len(); i++ {
		current, ok := ds.Record(i)
		if !ok {
			return nil, false, fmt.Errorf("dataset record %d disappeared", i)
		}
		key, err := datasetRecordKey(current)
		want, exists := desired[key]
		if err != nil || !exists {
			stale = append(stale, i)
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			stale = append(stale, i)
			continue
		}
		seen[key] = struct{}{}
		equal, err := sameDatasetRecord(current, want)
		if err != nil {
			return nil, false, err
		}
		if !equal {
			ds.Update(i, dataset.RecordUpdate{
				Input:          want.Input,
				ExpectedOutput: want.ExpectedOutput,
				Metadata:       want.Metadata,
			})
		}
	}
	var missing []dataset.Record
	for _, key := range order {
		if _, exists := seen[key]; !exists {
			missing = append(missing, desired[key])
		}
	}

	// Reuse stale records before appending new ones. This migrates older schemas
	// as updates and avoids mixing a full delete with a full insert in one batch.
	reused := min(len(stale), len(missing))
	for i := 0; i < reused; i++ {
		want := missing[i]
		ds.Update(stale[i], dataset.RecordUpdate{
			Input:          want.Input,
			ExpectedOutput: want.ExpectedOutput,
			Metadata:       want.Metadata,
		})
	}
	for i := len(stale) - 1; i >= reused; i-- {
		ds.Delete(stale[i])
	}
	if reused < len(missing) {
		ds.Append(missing[reused:]...)
	}
	if err := ds.Push(ctx); err != nil {
		return nil, false, fmt.Errorf("push dataset: %w", err)
	}
	ds, err = waitForDatasetRecords(ctx, name, project, desired)
	if err != nil {
		return nil, false, fmt.Errorf("validate synchronized dataset: %w", err)
	}
	return ds, false, nil
}

type pullDatasetFunc func(context.Context, string, ...dataset.PullOption) (*dataset.Dataset, error)

func waitForDatasetRecords(ctx context.Context, name, project string, desired map[string]dataset.Record) (*dataset.Dataset, error) {
	return pullUntilDatasetRecords(ctx, name, project, desired, dataset.Pull, 8, func(attempt int) time.Duration {
		return time.Duration(1<<attempt) * 250 * time.Millisecond
	})
}

// pullUntilDatasetRecords waits for newly created or updated records to become
// visible before an experiment pins the dataset version.
func pullUntilDatasetRecords(ctx context.Context, name, project string, desired map[string]dataset.Record, pull pullDatasetFunc, attempts int, retryDelay func(int) time.Duration) (*dataset.Dataset, error) {
	var validateErr error
	for attempt := 0; attempt < attempts; attempt++ {
		ds, err := pull(ctx, name, dataset.WithPullProjectName(project))
		if err == nil {
			validateErr = validateDatasetRecords(ds, desired)
			if validateErr == nil {
				return ds, nil
			}
		} else {
			validateErr = err
		}
		if attempt == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryDelay(attempt)):
		}
	}
	return nil, fmt.Errorf("after retries: %w", validateErr)
}

func validateDatasetRecords(ds *dataset.Dataset, desired map[string]dataset.Record) error {
	if ds.Len() != len(desired) {
		return fmt.Errorf("got %d records, want %d", ds.Len(), len(desired))
	}
	seen := make(map[string]struct{}, len(desired))
	for i, current := range ds.Records() {
		key, err := datasetRecordKey(current)
		if err != nil {
			return fmt.Errorf("record %d (%s): %w", i, current.ID(), err)
		}
		want, exists := desired[key]
		if !exists {
			return fmt.Errorf("record %d (%s) has unexpected key %q", i, current.ID(), key)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("record %d (%s) duplicates key %q", i, current.ID(), key)
		}
		seen[key] = struct{}{}
		equal, err := sameDatasetRecord(current, want)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("record %d (%s) differs from authored data", i, current.ID())
		}
	}
	return nil
}

func datasetRecordKey(rec dataset.Record) (string, error) {
	input, err := agenteval.DecodeTaskInput(rec)
	if err != nil {
		return "", err
	}
	return input.TaskID + "\x00" + input.PromptID, nil
}

func sameDatasetRecord(left, right dataset.Record) (bool, error) {
	leftJSON, err := canonicalJSON([]any{left.Input, left.ExpectedOutput, left.Metadata})
	if err != nil {
		return false, err
	}
	rightJSON, err := canonicalJSON([]any{right.Input, right.ExpectedOutput, right.Metadata})
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftJSON, rightJSON), nil
}

func canonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

func runVerify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	var c commonFlags
	c.bind(fs)
	refs := fs.String("refs", "HEAD", "comma-separated refs to check the mutations against")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Verify is the one command that can span every suite: it is the staleness
	// check, and checking one suite while another rots is the failure it exists
	// to prevent.
	selected, err := c.selectedSuites()
	if err != nil {
		return err
	}
	if err := c.resolveRepo(); err != nil {
		return err
	}

	for _, ref := range strings.Split(*refs, ",") {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		sha, err := agenteval.ResolveRef(ctx, c.repoDir, ref)
		if err != nil {
			return err
		}
		for _, suite := range selected {
			dir := filepath.Join(mustSelfDir(), "mutations", suite.Name)
			specs := suite.Specs()
			if err := agenteval.VerifyMutations(ctx, c.repoDir, dir, sha, specs); err != nil {
				return fmt.Errorf("suite %s: %w", suite.Name, err)
			}
			fmt.Printf("ok: %s, %d mutations apply at %s (%s)\n", suite.Name, len(specs), ref, sha[:12])
		}
	}
	return nil
}

func runCompare(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	var c commonFlags
	c.bind(fs)

	baselineRef := fs.String("baseline-ref", "", "baseline git ref, normally main")
	candidateRef := fs.String("candidate-ref", "", "candidate git ref, normally a branch head")
	pr := fs.Int("pr", 0, "resolve both refs from a GitHub PR number via gh")
	selfCheck := fs.Bool("self-check", false, "A/A run: use the baseline ref for both sides to measure the noise floor")
	label := fs.String("label", "", "candidate experiment suffix, e.g. pr5052 (defaults to the PR or refs)")

	model := fs.String("model", "", "model to pin for every run (defaults by agent)")
	runs := fs.Int("runs", 1, "runs per record")
	concurrency := fs.Int("concurrency", 2, "concurrent agent sessions")
	tasks := fs.String("tasks", "", "comma-separated task IDs to run (default: every task in the suite)")

	agentTimeout := fs.Duration("agent-timeout", 45*time.Minute, "per-session timeout")
	validationTimeout := fs.Duration("validation-timeout", 30*time.Minute, "per-validation-command timeout")
	resultsDir := fs.String("results-dir", "", "where to write artifacts (defaults to _eval/results/<label>)")
	keepWorkspaces := fs.Bool("keep-workspaces", false, "keep materialised trees for inspection (a full repo copy per run)")
	agent := fs.String("agent", "all", "comma-separated coding agents: claude,codex (all runs both)")
	claudeImage := fs.String("claude-image", "dd-trace-go-agent-eval/claude:2.1.231", "Claude container image")
	codexImage := fs.String("codex-image", "dd-trace-go-agent-eval/codex:0.147.0", "Codex container image")
	containerCPUs := fs.Int64("container-cpus", 4, "CPU limit per agent container")
	containerMemoryGiB := fs.Int64("container-memory-gib", 4, "memory limit in GiB per agent container")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := c.resolve(); err != nil {
		return err
	}
	agentNames, err := selectedAgents(*agent)
	if err != nil {
		return err
	}
	if len(agentNames) > 1 && *model != "" {
		return fmt.Errorf("-model requires selecting one agent with -agent")
	}
	if *containerCPUs <= 0 {
		return fmt.Errorf("-container-cpus must be greater than zero")
	}
	if *containerMemoryGiB <= 0 {
		return fmt.Errorf("-container-memory-gib must be greater than zero")
	}
	containerResources := agenteval.ContainerResources{
		CPUs:      *containerCPUs,
		MemoryGiB: *containerMemoryGiB,
	}

	baselineName := *baselineRef
	candidateName := *candidateRef
	if *pr != 0 {
		refs, err := prRefs(ctx, *pr)
		if err != nil {
			return err
		}
		*baselineRef, *candidateRef = refs.BaseSHA, refs.HeadSHA
		baselineName, candidateName = refs.BaseName, refs.HeadName
		if *label == "" {
			*label = fmt.Sprintf("pr%d", *pr)
		}
	}
	if *baselineRef == "" {
		return fmt.Errorf("-baseline-ref or -pr is required")
	}
	if baselineName == "" {
		baselineName = *baselineRef
	}
	baseSHA, err := agenteval.ResolveRef(ctx, c.repoDir, *baselineRef)
	if err != nil {
		return err
	}
	candSHA := baseSHA
	if !*selfCheck {
		if *candidateRef == "" {
			return fmt.Errorf("-candidate-ref is required unless -self-check is set")
		}
		if candSHA, err = agenteval.ResolveRef(ctx, c.repoDir, *candidateRef); err != nil {
			return err
		}
		if candidateName == "" {
			candidateName = *candidateRef
		}
	} else {
		candidateName = baselineName
	}
	if *label == "" {
		if *selfCheck {
			*label = "selfcheck-" + baseSHA[:12]
		} else {
			*label = baseSHA[:7] + "-vs-" + candSHA[:7]
		}
	}
	if *resultsDir == "" {
		*resultsDir = filepath.Join(mustSelfDir(), "results", *label)
	}

	stop, err := startLLMObs(&c)
	if err != nil {
		return err
	}
	defer stop()

	selectedTasks, err := selectTasks(c.suite, splitList(*tasks))
	if err != nil {
		return err
	}
	taskDatasets, err := syncTaskDatasets(ctx, &c, selectedTasks)
	if err != nil {
		return err
	}

	var runErrors []error
	for _, agentName := range agentNames {
		modelName := *model
		if modelName == "" {
			modelName, err = defaultModel(agentName)
			if err != nil {
				return err
			}
		}
		var runner agenteval.Runner
		switch agentName {
		case "claude":
			runner = &agenteval.ClaudeRunner{Image: *claudeImage, ModelName: modelName, ContainerResources: containerResources}
		case "codex":
			runner = &agenteval.CodexRunner{Image: *codexImage, ModelName: modelName, ContainerResources: containerResources}
		}
		cfg := agenteval.ComparisonConfig{
			RepoDir:           c.repoDir,
			MutationsDir:      c.mutationsDir(),
			ResultsDir:        filepath.Join(*resultsDir, agentName),
			ProjectName:       c.projectName,
			SuiteName:         c.suite.Name,
			ExtraEvaluators:   c.suite.Evaluators,
			TaskDatasets:      taskDatasets,
			Specs:             c.suite.Specs(),
			Label:             *label,
			BaselineRef:       baseSHA,
			BaselineName:      baselineName,
			CandidateRef:      candSHA,
			CandidateName:     candidateName,
			Runner:            runner,
			Runs:              *runs,
			Concurrency:       *concurrency,
			Tasks:             splitList(*tasks),
			AgentTimeout:      *agentTimeout,
			ValidationTimeout: *validationTimeout,
			KeepWorkspaces:    *keepWorkspaces,
		}

		cmp, runErr := agenteval.RunComparison(ctx, cfg)
		if cmp != nil {
			for _, result := range cmp.Experiments {
				fmt.Printf("experiment %s %s (%s): %s\n", result.TaskID, result.Side, result.Branch, result.URL)
			}
		}
		if runErr != nil {
			runErrors = append(runErrors, fmt.Errorf("%s: %w", agentName, runErr))
		}
		if ctx.Err() != nil {
			break
		}
	}
	return errors.Join(runErrors...)
}

func selectedAgents(agent string) ([]string, error) {
	requested := splitList(agent)
	if len(requested) == 0 {
		requested = []string{"all"}
	}
	for _, name := range requested {
		if name == "all" {
			return []string{"claude", "codex"}, nil
		}
	}
	var selected []string
	for _, name := range requested {
		if name != "claude" && name != "codex" {
			return nil, fmt.Errorf("unsupported -agent value %q: choose all, claude, or codex", name)
		}
		if !slices.Contains(selected, name) {
			selected = append(selected, name)
		}
	}
	return selected, nil
}

func selectTasks(suite *suites.Suite, taskIDs []string) ([]agenteval.Task, error) {
	selected := make([]agenteval.Task, 0, len(suite.Tasks))
	found := make(map[string]bool, len(taskIDs))
	for _, task := range suite.Tasks {
		if len(taskIDs) == 0 || slices.Contains(taskIDs, task.Spec.TaskID) {
			selected = append(selected, task)
			found[task.Spec.TaskID] = true
		}
	}
	for _, taskID := range taskIDs {
		if !found[taskID] {
			return nil, fmt.Errorf("unknown task %q in suite %s", taskID, suite.Name)
		}
	}
	return selected, nil
}

func defaultModel(agent string) (string, error) {
	switch agent {
	case "claude":
		return defaultClaudeModel, nil
	case "codex":
		return defaultCodexModel, nil
	default:
		return "", fmt.Errorf("unsupported -agent %q: choose claude or codex", agent)
	}
}

type pullRequestRefs struct {
	BaseSHA  string `json:"baseRefOid"`
	HeadSHA  string `json:"headRefOid"`
	BaseName string `json:"baseRefName"`
	HeadName string `json:"headRefName"`
}

// prRefs resolves a PR to immutable SHAs and the branch names used in the
// experiment names.
func prRefs(ctx context.Context, number int) (pullRequestRefs, error) {
	out, err := exec.CommandContext(ctx, "gh", "pr", "view", fmt.Sprint(number),
		"--json", "baseRefOid,headRefOid,baseRefName,headRefName").Output()
	if err != nil {
		return pullRequestRefs{}, fmt.Errorf("gh pr view %d: %w", number, err)
	}
	var parsed pullRequestRefs
	if err := json.Unmarshal(out, &parsed); err != nil {
		return pullRequestRefs{}, err
	}
	if parsed.BaseSHA == "" || parsed.HeadSHA == "" || parsed.BaseName == "" || parsed.HeadName == "" {
		return pullRequestRefs{}, fmt.Errorf("gh returned incomplete refs for PR %d", number)
	}
	return parsed, nil
}

// mustSelfDir returns the _eval directory, so mutations and results resolve the
// same way whether the command is run via `go run ./cmd/agent-eval` or an
// installed binary.
func mustSelfDir() string {
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; {
			if filepath.Base(dir) == "_eval" {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "."
}

// splitList parses a comma-separated flag into a trimmed slice, dropping empties.
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
