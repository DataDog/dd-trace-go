# `_eval`: agent eval harness

Measures whether a change to this repository makes coding agents better at real
dd-trace-go tasks.

It compares **two git revisions**, normally `main` against a branch head. Each task
and revision becomes one Datadog LLM Obs experiment. The dataset points are prompt
variants for that task.

This is a **local developer tool**. There is no CI entry point and it gates nothing.

## Why this exists

Documentation and skill changes used to land on judgement alone. There was no way to
answer "does this branch actually help an agent?" This harness answers it for one
branch at a time.

Nothing in the harness knows what the thing under test is. It compares two refs,
and what differs between them is the caller's business: documentation, an
`AGENTS.md` tree, a skill, a setup script, a repo convention.

## Suites

A **suite** is a set of tasks that share a question:

```shell
make suites          # or: go run ./cmd/agent-eval suites
```

| Suite | Question |
| --- | --- |
| `integration-authoring` | Does this change help an agent build and wire up a contrib integration? |

Each task has a separate LLM Obs dataset whose records are its prompt variants.
The dataset name combines the suite prefix and task ID. Every command takes
`-suite`.

One suite exists today. Others worth having: fixing a bug from a reproducer,
de-flaking a test, adding a package or module, upgrading a dependency. The
harness needs no changes for any of them, only a file under `suites/`. See
[Adding a suite](#adding-a-suite).

## Layout

```
_eval/
  cmd/agent-eval/        CLI
  internal/agenteval/    harness: workspaces, mutations, runners, and criteria
  docker/                pinned Claude Code and Codex container images
  suites/                the tasks, as reviewable Go. Add yours here.
  mutations/<suite>/     *.patch files, for tasks that use the apply_patch mutation
  results/               run artifacts (gitignored)
```

The local `Makefile` provides the common harness commands:

```shell
cd _eval
make help
make test
make build
make images
```

## Setup

```shell
export DD_API_KEY=...     # required
export DD_APP_KEY=...     # required in agentless mode, which is the usual local case
export DD_SITE=datadoghq.com
```

`DD_APP_KEY` is only needed when there is no local Datadog agent with EVP proxy v2.
For a laptop run, assume it is needed.

Docker must be running. Build the pinned Claude Code and Codex images once, or
after changing their versions:

```shell
make images
make images/check
make test/container
```

The host agent CLIs are not used. Claude authentication comes from
`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, or the managed `apiKeyHelper`.
Codex authentication comes from `$CODEX_AUTH_FILE`, `$CODEX_HOME/auth.json`, or
`~/.codex/auth.json`. The harness passes only this authentication material into
the selected container.

## Use

Validate or push a suite:

```shell
go run ./cmd/agent-eval seed -suite integration-authoring -dry-run  # validate, no push
go run ./cmd/agent-eval seed -suite integration-authoring
```

`compare` synchronizes every selected task dataset before starting experiments.
Existing records are matched by task and prompt. Datadog keeps the dataset
version unchanged when the content has not changed. Each baseline and candidate
experiment is pinned to the version returned by that synchronization.

Check that every task mutation still applies. Mutations reference real paths, so they
rot as `main` advances. `-suite all` checks every suite, which is what `make verify`
runs:

```shell
go run ./cmd/agent-eval verify -suite all -refs main,HEAD
```

**Measure the noise floor before measuring anything else.** An A/A run puts the same
commit on both sides, so the true difference is zero by construction and whatever
delta comes out is this suite's noise:

```shell
go run ./cmd/agent-eval compare -suite integration-authoring \
  -self-check -baseline-ref origin/main \
  -tasks orchestrion-aspect-valkey-go,orchestrion-aspect-franz-go,valkey-option-conventions \
  -model <pinned-model> -runs 3
```

`-tasks` matters for cost. Every selected task creates one experiment per revision
and runs all of its prompt records. The two anchors run for 20 to 30 minutes and cost
several dollars each. Omit `-tasks` only when you want the whole suite.

Then compare a branch. Without the A/A number from the previous step there is no
honest way to read the result:

```shell
go run ./cmd/agent-eval compare -suite integration-authoring \
  -pr 5052 \
  -runs 5 -concurrency 4
```

The default runs both Claude and Codex, one after the other, using
`claude-sonnet-5` and `gpt-5.6-terra` with medium effort. `-agent` accepts a comma-separated list,
such as `-agent claude`, `-agent codex`, or `-agent claude,codex`. The special
value `all` selects both. `-model` requires selecting one agent. `-claude-image`
and `-codex-image` override the default pinned images. Each agent container is
limited to 4 CPUs and 4 GiB of memory by default. Use `-container-cpus` and
`-container-memory-gib` to override those limits.

`-pr` resolves both SHAs through `gh`. Exact SHAs matter, because `main` moves while a
multi-hour comparison is in flight.

## How a run works

For each selected task, the harness creates separate baseline and candidate
experiments. The baseline name ends with its branch, normally `main`. The
candidate name ends with the comparison label, normally a PR such as `pr5052`.
The agent name appears immediately before that suffix. Every pair uses one
task-specific dataset and the same configured prompt variants.
For example, the CloudEvents dataset has three records, and both experiments run
those three records.

For each record:

1. Materialise the tree at the ref with `git archive` into a fresh single-commit
   repo. An archive rather than a clone, because a clone leaves the original commit
   reachable and an agent asked to rebuild deleted code can just read it back out of
   history.
2. Apply the task mutation, which removes the work the agent has to redo. Identical
   on both sides, or the two sides are not the same task.
3. Commit, so the diff collected later describes only the agent's work.
4. Run the selected agent headless in an isolated container.
5. Collect changed paths and keep the full diff and transcript as local artifacts.
6. Run the task's validation commands.
7. Score every criterion that applies to that task.

Each container run creates an aggregate LLM Obs span with the prompt, final agent
response, model, token usage, estimated cost, task, branch, and commit. It covers
the whole coding-agent session. Claude and Codex may make several internal model
and tool calls, but those calls are not individually visible because the CLIs do
not export their tracing context or per-call telemetry to the harness.

Criteria a task does not declare are left absent rather than false. Since each
task now has its own experiments, unrelated criteria are not added as evaluator
columns for that task.

## Experiment record contract

Treat every experiment record as a test case:

- `input` contains only the user-visible task ID, prompt ID, and prompt.
- `expected_output` contains observable expectations such as changed paths,
  validation results, checks, and their expected totals, passed counts, and scores.
- `output` contains the corresponding observed values from the run.
- Mutation details, validation commands, path restrictions, and source patterns
  stay in the typed Go task definition. Do not upload harness configuration as
  record input or metadata.
- Full diffs, generated source, command output, and transcripts stay under
  `_eval/results/`. Do not put them in experiment input, output, or expected output.

When adding an experiment, use the same field names for expected and actual
values. A useful field must support a direct comparison. For example,
`expected_output.validations.tests: true` corresponds to
`output.validations.tests`, and `expected_output.changed_paths` corresponds to
`output.changed_paths`.

## Reading results

Use the Datadog experiment page. Each task and revision has its own experiment
URL. Names follow `<task>-<agent>-<main-or-pr>`, and tags include `side`, `branch`,
and `ref`. Compare baseline and candidate experiments for the same task,
then compare rows with the same `prompt_id`.

Checks and validations are summarized separately. `checks_total` and
`checks_passed` count applicable checks and full passes. `checks_score` is their
weighted score from 0 to 1. A task can assign larger weights to checks that are
more important to a correct implementation. Checks without a declared weight
have weight 1.

Path checks support partial credit. `expected_paths` is the fraction of expected
paths that changed. `forbidden_paths` is `1 / (1 + forbidden files changed)`, so
zero forbidden files scores 1, one scores 0.5, and two score about 0.33.

`validation_total` and `validation_passed` stay in output, while
`validation_score` is the unweighted validation ratio. Individual results stay
in `output.checks` and `output.validations`. Process observations such as agent
exit, permission denials, documentation access, and upstream access stay in
`output.diagnostics` and do not affect either score. The branch appears in the
experiment name, tags, config, and output.

The harness retries a container or agent CLI infrastructure failure once in a
fresh workspace. If the retry also fails, the record has status
`infrastructure_failure` and its numeric evaluators are left empty.

Per-criterion deltas, not one pooled pass rate. At the sample sizes this harness
runs at, a single rate cannot separate signal from noise, but a dozen criteria can
point at which one moved and therefore which part of a change did the work.

Roughly 35 samples per side puts a binary pass rate at about plus or minus 15
points. Detecting a 15 point improvement with confidence needs on the order of 150
runs per side, which is not affordable. So: treat output as directional, always
against a measured noise floor, and get sensitivity from many criteria per run
rather than from many runs.

## Isolation

Every agent session runs in a fresh container managed by Testcontainers for Go.
The host home directory is not mounted and the container gets a new temporary
home. User settings, skills, memories, hooks, plugins, MCP servers, and caches are
not copied into it.

The only writable mounts are the throwaway task workspace and an in-memory `/tmp`.
The image root filesystem is read only. The process runs as the host UID with all
Linux capabilities dropped, `no-new-privileges`, and CPU, memory, and process
limits. The container is removed after the session.

Claude runs with an empty strict MCP configuration, slash commands disabled, and
no session persistence. Its temporary config trusts `/workspace`, so checked-in
project settings and hooks apply without an interactive trust prompt. Codex runs
ephemerally with user config, rules, memories, plugins, and multi-agent support
disabled. Both CLIs still receive flags that bypass their own approval sandbox
because the container is the security boundary.

Your selected agent credential is readable by that agent process. Claude receives
its token through the container environment. Codex receives a read-only mount of
the host auth file, then copies it into its temporary home. The harness does not
write credentials to run artifacts, but an agent command could print them into its
transcript.

Container networking remains enabled for model calls and dependency downloads.
There is no hostname allowlist. Upstream lookups are **detected**
(`no_upstream_fetch`) rather than prevented.

Claude reports token usage and cost in its terminal result event. Codex reports
token usage in `turn.completed`; the harness estimates GPT-5.6 Terra cost from
the [published model rates](https://developers.openai.com/api/docs/models/gpt-5.6-terra).
The harness sends standard token metrics on a child LLM span, which provides the
built-in token and estimated-cost columns. The harness does not add summary
evaluators because the UI can aggregate the per-record metrics.

## Criteria

Three kinds, all deterministic:

- **Validation commands**: shell commands run in the finished workspace and
  recorded in `output.validations`. Does it compile, do the tests pass.
- **Source checks**: a named regexp that must, or must not, match the files a
  glob selects (`option_fn`, `no_legacy_naming_map`, ...). Results live in
  `output.checks`. This is where conventions live: things that compile perfectly
  and are still wrong.
- **Structural checks**: `required_paths_present`, `orchestrion_aspect_present`,
  `expected_paths`, and `forbidden_paths`.
- **Diagnostics**: process and observation data that helps explain a run but does
  not contribute to `checks_score`.

Labels are shared across records on purpose. Two tasks declaring `option_fn`
produce the same key in their output and expected output.

Most of the convention criteria were derived from a real review of
[#5156](https://github.com/DataDog/dd-trace-go/pull/5156), which added the
cloudevents integration. Every review finding that reduced to a mechanical check
became a source check, and each one maps to a section of the documentation under
test. Review comments on integration PRs are the best source of tasks there is:
they are already a list of what an author gets wrong, written by someone who knew
what right looked like.

## Adding a suite

Add a file under `suites/` that registers itself. `suites/integration_authoring.go`
is the worked example; the task functions in it cover every mutation kind and
every kind of criterion.

```go
func init() {
	Register(&Suite{
		Name:        "flaky-tests",
		Dataset:     "dd-trace-go-flaky-tests", // per-task dataset prefix
		Description: "Tasks covering diagnosing and fixing a flaky test.",
		Docs:        []string{"CONTRIBUTING.md"},
		Tasks:       []agenteval.Task{deflakeSomeTest()},
	})
}
```

Then `seed -suite flaky-tests` and it is usable. Nothing else needs touching: the
CLI, criteria, experiments, and staleness checks all discover it.

### Writing a task

Tasks are typed Go, not maps, so a misspelled field is a compile error rather
than a criterion that silently never fires:

```go
agenteval.Task{
	Spec: agenteval.TaskSpec{
		TaskID:   "deflake-partial-flush",
		Prompt:   "...",
		Mutation: agenteval.Mutation{Kind: agenteval.MutationApplyPatch, Patch: "deflake-partial-flush.patch"},
		ValidationCommands: []agenteval.ValidationCommand{
			{Label: "repeat", Command: "go test -race -count=50 -run TestPartialFlush ./ddtrace/tracer/"},
		},
		ForbiddenPaths:   CommonForbidden,
		MaxDiffLines:     200,
		DocsExpectedRead: []string{"CONTRIBUTING.md"},
		UpstreamMarkers:  []string{"..."},
		CheckWeights: map[string]float64{
			"no_sleep_synchronisation": 2,
		},
		SourceChecks: []agenteval.SourceCheck{
			Absent("no_sleep_synchronisation", `time\.Sleep\(`, "ddtrace/tracer/*_test.go"),
		},
	},
	Metadata: agenteval.TaskMetadata{
		Category:    "flaky_tests",
		FailureMode: "timing_dependent_test",
		Size:        agenteval.SizeSmall,
		Source:      "https://github.com/DataDog/dd-trace-go/pull/4929",
	},
}
```

By default, `Spec.Prompt` creates one prompt named `default`. To compare prompt
wording, leave `Spec.Prompt` empty and add variants to the task. Every variant is
run in both branch experiments:

```go
Prompts: []agenteval.PromptVariant{
	{ID: "add-support", Prompt: "Add support for github.com/cloudevents/sdk-go/v2 in dd-trace-go."},
	{ID: "new-integration", Prompt: "Create a new integration for github.com/cloudevents/sdk-go/v2."},
	{
		ID: "follow-repo-guidance",
		Prompt: "Create a new integration for github.com/cloudevents/sdk-go/v2. " +
			"Find any repository guidance for writing integrations and follow every step and convention rigorously.",
	},
},
```

Pick the mutation by what has to be taken away: `delete_paths` for whole files,
`apply_patch` for something smaller, `none` with `assert_absent` for a task
asking for something the repo does not have yet. See
[`mutations/README.md`](./mutations/README.md).

### The rules that matter

**The task must have a failure mode the change under test addresses.** For a
docs-only diff, the two sides differ only in files the agent may or may not read.
A generic task scores the same on both sides and measures nothing.
`TestSuitesAreDocSensitive` enforces the mechanical part, but it cannot check that
a criterion is one the docs actually explain. That part is on you.

**Start from a real review.** Comments on a merged PR are already a list of what
someone got wrong, written by someone who knew what right looked like. Record
which PR in `Metadata.Source` so the next reader can check the criterion against
it.

**Prefer several small tasks over one large reconstruction.** A reconstruction
exercises twenty things at once, so the signal is diluted, and it costs hours per
run. Pick a subset with `-tasks`, not by ordering: records come back from the
backend in its own order, so declaration order does not decide what runs.

**Check what a criterion implies about the rest of the repo.** The
integration-authoring convention criteria are scored only on tasks that create
something new, because every existing entry in `instrumentation/packages.go`
carries a `naming` map and neither franz-go nor valkey-go has `WithCustomTag`.
Scoring them on a reconstruction of either would mark the repository's own code
as wrong.

### Custom criteria

Validation commands and source checks cover most things. When they genuinely do
not, a suite can contribute Go evaluators:

```go
Evaluators: []experiment.Evaluator{
	agenteval.ScoreEvaluator("retries_needed", func(o *agenteval.AgentRunOutput) float64 { ... }),
},
```

Prefer data over code here. A criterion expressed as a source check is reviewable
by someone who does not read Go, and it needs no harness change.
