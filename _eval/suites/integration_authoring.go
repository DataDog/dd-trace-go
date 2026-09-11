// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package suites

import (
	"regexp"

	"github.com/DataDog/dd-trace-go/_eval/internal/agenteval"
)

// regexpQuote escapes a literal for use inside a source-check pattern.
func regexpQuote(s string) string { return regexp.QuoteMeta(s) }

// The integration-authoring suite: does a change make an agent better at
// building and wiring up a contrib integration?
//
// Most of the convention criteria below were derived from the review of
// PR #5156, which added the cloudevents integration. Every finding on that PR
// that reduced to a mechanical check became a criterion here, and each maps to a
// section of the documentation under test:
//
//	option_fn, option_iface          INTEGRATIONS.md section 4
//	custom_tag_option               INTEGRATIONS.md section 4
//	service_source_*                INTEGRATIONS.md section 5
//	no_legacy_naming_*              INTEGRATIONS.md section 5
//	semconv_event_tags              INTEGRATIONS.md section 6
//	table_driven_subtests           INTEGRATIONS.md section 10
//	package_example                 INTEGRATIONS.md section 10
//	orchestrion_aspect_present, required_paths_present  INTEGRATIONS.md section 9
//
// That correspondence is the point. A criterion the docs do not address cannot
// move between the two sides of a docs comparison, so it costs a column and
// tells you nothing.
//
// Review comments on real integration PRs are the best source of tasks there is:
// they are already a list of what an author gets wrong, written by someone who
// knew what right looked like. When you add tasks here, start from one.

// upstreamMarkers are the things that, appearing in a fetch, mean the agent went
// looking for the answer instead of writing it: dd-trace-go's own published
// source, and its docs.
//
// They are deliberately not the instrumented package's import path. That path is
// a legitimate dependency the agent must reference, so using it as a marker
// flagged every run as contaminated.
var upstreamMarkers = []string{
	"github.com/DataDog/dd-trace-go",
	"pkg.go.dev/github.com/DataDog/dd-trace-go",
	"raw.githubusercontent.com/DataDog/dd-trace-go",
}

func init() {
	Register(&Suite{
		Name:    "integration-authoring",
		Dataset: "dd-trace-go-integration-authoring",
		Description: "Doc-sensitive tasks covering contrib integration authoring: " +
			"Orchestrion aspects, option conventions, naming, registration, and tests.",
		Docs: []string{"contrib/INTEGRATIONS.md", "contrib/ORCHESTRION.md", "contrib/AGENTS.md"},
		// Ordered cheapest first for readability only. Selecting a subset is
		// -tasks; declaration order does not survive the round trip through the
		// backend.
		Tasks: []agenteval.Task{
			orchestrionAspect("orchestrion-aspect-valkey-go", "valkey-io/valkey-go",
				"contrib/valkey-io/valkey-go"),
			orchestrionAspect("orchestrion-aspect-franz-go", "twmb/franz-go",
				"contrib/twmb/franz-go"),
			optionConventions(),
			registerInPackagesGo(),
			orchestrionIntegrationTest(),
			contribTests(),
			reconstructFranzGo(),
			authorCloudEvents(),
		},
	})
}

// orchestrionAspect removes an integration's aspect file. The code still builds
// and its tests still pass; auto-instrumentation just silently does nothing,
// which is what makes this worth a task.
func orchestrionAspect(taskID, pkg, contribDir string) agenteval.Task {
	aspect := contribDir + "/orchestrion.yml"
	return agenteval.Task{
		Spec: agenteval.TaskSpec{
			TaskID: taskID,
			Prompt: "The " + pkg + " integration is not picked up by Orchestrion " +
				"compile-time auto-instrumentation. Fix it.",
			Mutation: agenteval.Mutation{
				Kind:  agenteval.MutationDeletePaths,
				Paths: []string{aspect},
			},
			ValidationCommands:   GoModuleChecks(contribDir),
			ExpectedChangedPaths: []string{aspect},
			ForbiddenPaths:       CommonForbidden,
			MaxDiffLines:         200,
			DocsExpectedRead:     []string{"contrib/ORCHESTRION.md", "contrib/AGENTS.md"},
			OrchestrionYAML:      aspect,
			UpstreamMarkers:      upstreamMarkers,
			// Without these the task only asks "is there a parseable file
			// there", which the agent manages every time. The aspect has to
			// actually attach to something and inject the integration.
			SourceChecks: []agenteval.SourceCheck{
				Present("aspect_has_join_point", `join-point:`, aspect),
				Present("aspect_injects_contrib", regexpQuote(contribDir), aspect),
			},
		},
		Metadata: agenteval.TaskMetadata{
			Category:    "orchestrion_registration",
			FailureMode: "missing_aspect",
			Size:        agenteval.SizeSmall,
		},
	}
}

// optionConventions deletes an integration's options file. The agent rewrites it
// with only the surrounding code and the docs to go on, and the tree is full of
// older integrations using the pre-docs shape, which is the pull the
// documentation has to overcome. That is the failure mode #5156 hit.
//
// The reference solution here is the docs, not the deleted file: valkey-go's own
// option.go predates the Option/OptionFn convention.
func optionConventions() agenteval.Task {
	const contribDir = "contrib/valkey-io/valkey-go"
	goFiles := contribDir + "/*.go"
	return agenteval.Task{
		Spec: agenteval.TaskSpec{
			TaskID: "valkey-option-conventions",
			Prompt: "The valkey-io/valkey-go integration is missing its configuration " +
				"options. Restore them.",
			Mutation: agenteval.Mutation{
				Kind:  agenteval.MutationDeletePaths,
				Paths: []string{contribDir + "/option.go"},
			},
			ValidationCommands:   GoModuleChecks(contribDir),
			ExpectedChangedPaths: []string{contribDir + "/"},
			ForbiddenPaths:       CommonForbidden,
			MaxDiffLines:         300,
			DocsExpectedRead:     []string{"contrib/INTEGRATIONS.md", "contrib/AGENTS.md"},
			UpstreamMarkers:      upstreamMarkers,
			SourceChecks: []agenteval.SourceCheck{
				Present("option_iface", `(?m)^type Option interface\b`, goFiles),
				Present("option_fn", `(?m)^type OptionFn\b`, goFiles),
				Present("service_source_option_override", `ServiceSourceWithServiceOption`, goFiles),
			},
		},
		Metadata: agenteval.TaskMetadata{
			Category:    "integration_options",
			FailureMode: "non_conventional_options",
			Size:        agenteval.SizeSmall,
			Source:      "https://github.com/DataDog/dd-trace-go/pull/5156",
		},
	}
}

// registerInPackagesGo strips one integration's entry from the registration
// table. Nothing fails to compile: instrumentation.Load just cannot find the
// package at runtime, so telemetry and the component tag silently go missing.
//
// This is the first failure mode INTEGRATIONS.md section 7 addresses, and until
// this task existed the registered_in_packages_go criterion had no record that
// declared it and therefore never ran.
func registerInPackagesGo() agenteval.Task {
	const contribDir = "contrib/valkey-io/valkey-go"
	return agenteval.Task{
		Spec: agenteval.TaskSpec{
			TaskID: "register-valkey-packages-go",
			Prompt: "The valkey-io/valkey-go integration reports no telemetry and its " +
				"component tag is missing. Fix it.",
			Mutation: agenteval.Mutation{
				Kind:  agenteval.MutationApplyPatch,
				Patch: "register-valkey-packages-go.patch",
			},
			ValidationCommands:   GoModuleChecks(contribDir),
			ExpectedChangedPaths: []string{"instrumentation/packages.go"},
			ForbiddenPaths:       CommonForbidden,
			MaxDiffLines:         120,
			DocsExpectedRead:     []string{"contrib/INTEGRATIONS.md", "contrib/AGENTS.md"},
			RegistrationImport:   "github.com/valkey-io/valkey-go",
			UpstreamMarkers:      upstreamMarkers,
			SourceChecks: []agenteval.SourceCheck{
				// INTEGRATIONS.md section 5 says new entries carry no naming
				// map. The neighbouring entries all have one, so copying a
				// neighbour is the wrong answer and the docs are what say so.
				Absent("no_legacy_naming_map",
					`PackageValkeyIoValkeyGo[\s\S]{0,400}?(naming:|buildOpName|buildServiceName)`,
					"instrumentation/packages.go"),
			},
		},
		Metadata: agenteval.TaskMetadata{
			Category:    "orchestrion_registration",
			FailureMode: "missing_registration",
			Size:        agenteval.SizeSmall,
			Source:      "https://github.com/DataDog/dd-trace-go/pull/5156",
		},
	}
}

// orchestrionIntegrationTest removes the auto-instrumentation test package for an
// integration that keeps its orchestrion.yml. Nothing fails to build without it,
// which is exactly why it gets forgotten.
func orchestrionIntegrationTest() agenteval.Task {
	const testDir = "internal/orchestrion/_integration/twmb_franz_go"
	return agenteval.Task{
		Spec: agenteval.TaskSpec{
			TaskID: "orchestrion-test-franz-go",
			Prompt: "Add an end-to-end test for Orchestrion auto-instrumentation of the " +
				"twmb/franz-go integration.",
			Mutation: agenteval.Mutation{
				Kind:  agenteval.MutationDeletePaths,
				Paths: []string{testDir},
			},
			ValidationCommands: []agenteval.ValidationCommand{{
				Label:   "orchestrion_integration_vet",
				Command: "cd internal/orchestrion/_integration && go vet -tags integration ./twmb_franz_go/...",
			}},
			ExpectedChangedPaths: []string{testDir + "/"},
			ForbiddenPaths:       CommonForbidden,
			MaxDiffLines:         400,
			DocsExpectedRead:     []string{"contrib/ORCHESTRION.md", "contrib/INTEGRATIONS.md", "contrib/AGENTS.md"},
			RequiredPaths:        []string{testDir},
			UpstreamMarkers:      upstreamMarkers,
		},
		Metadata: agenteval.TaskMetadata{
			Category:    "orchestrion_registration",
			FailureMode: "missing_integration_test",
			Size:        agenteval.SizeSmall,
			Source:      "https://github.com/DataDog/dd-trace-go/pull/5156",
		},
	}
}

// contribTests deletes an integration's tests and asks for them back. The
// implementation stays, so this measures test-writing on its own rather than
// test-writing tangled up with getting the code right.
//
// INTEGRATIONS.md section 10 also asks for about 90% coverage, which is the
// criterion that would matter most here. It is not scored: `go test
// -coverprofile` reports 0.0% for these contrib modules even with -coverpkg, so
// a coverage gate would be measuring the build layout rather than the agent.
// Worth revisiting.
func contribTests() agenteval.Task {
	const contribDir = "contrib/valkey-io/valkey-go"
	const testFiles = contribDir + "/*_test.go"
	return agenteval.Task{
		Spec: agenteval.TaskSpec{
			TaskID: "write-tests-valkey-go",
			Prompt: "The valkey-io/valkey-go integration has no tests. Add appropriate tests.",
			Mutation: agenteval.Mutation{
				Kind: agenteval.MutationDeletePaths,
				Paths: []string{
					contribDir + "/valkey_test.go",
					contribDir + "/example_test.go",
				},
			},
			ValidationCommands:   GoModuleChecks(contribDir),
			ExpectedChangedPaths: []string{contribDir + "/"},
			// The implementation is the thing under test, not the thing to
			// change: an agent that edits it to make its own tests pass has
			// solved a different problem.
			ForbiddenPaths: append([]string{
				contribDir + "/valkey.go",
				contribDir + "/option.go",
			}, CommonForbidden...),
			MaxDiffLines:     800,
			DocsExpectedRead: []string{"contrib/INTEGRATIONS.md", "contrib/AGENTS.md"},
			UpstreamMarkers:  upstreamMarkers,
			SourceChecks: []agenteval.SourceCheck{
				Present("table_driven_subtests", `t\.Run\(`, testFiles),
				Present("package_example", `(?m)^func Example\(\)`, testFiles),
				Present("mocktracer_used", `mocktracer\.`, testFiles),
			},
		},
		Metadata: agenteval.TaskMetadata{
			Category:    "testing",
			FailureMode: "non_conventional_tests",
			Size:        agenteval.SizeSmall,
		},
	}
}

// reconstructFranzGo is a coarse end-to-end anchor: the implementation is
// deleted but the tests are kept, so the retained tests are the specification
// and `go vet` plus `go test` are real ground truth.
//
// It is one record rather than the bulk of the suite. A reconstruction exercises
// twenty things at once, so the signal is diluted, and each run costs hours. The
// small tasks are what carry the sensitivity.
func reconstructFranzGo() agenteval.Task {
	const contribDir = "contrib/twmb/franz-go"
	return agenteval.Task{
		Spec: agenteval.TaskSpec{
			TaskID: "reconstruct-franz-go",
			Prompt: "The twmb/franz-go integration is missing its implementation. Restore it.",
			Mutation: agenteval.Mutation{
				Kind: agenteval.MutationDeletePaths,
				Paths: []string{
					contribDir + "/kgo.go",
					contribDir + "/carrier.go",
					contribDir + "/option.go",
					contribDir + "/orchestrion.yml",
				},
			},
			ValidationCommands:   GoModuleChecks(contribDir),
			ExpectedChangedPaths: []string{contribDir + "/", contribDir + "/orchestrion.yml"},
			ForbiddenPaths: append([]string{
				contribDir + "/kgo_test.go",
				contribDir + "/option_test.go",
			}, CommonForbidden...),
			MaxDiffLines:     1500,
			DocsExpectedRead: []string{"contrib/INTEGRATIONS.md", "contrib/ORCHESTRION.md", "contrib/AGENTS.md"},
			OrchestrionYAML:  contribDir + "/orchestrion.yml",
			UpstreamMarkers:  upstreamMarkers,
		},
		Metadata: agenteval.TaskMetadata{
			Category:    "integration_authoring",
			FailureMode: "full_reconstruction",
			Size:        agenteval.SizeLarge,
		},
	}
}

// authorCloudEvents asks for an integration the repository does not have. It is
// the one task where every convention criterion applies at once, because there
// is no existing entry to copy and the documentation is the only guidance.
//
// The convention criteria are scored here and not on the reconstruction tasks
// for a reason: every existing entry in instrumentation/packages.go carries a
// naming map, and neither franz-go nor valkey-go has WithCustomTag, so scoring
// them on a reconstruction of either would mark the repository's own code as
// wrong.
//
// The integration is deleted when present so this task remains usable after
// PR #5156 lands. AllowMissing keeps it usable against earlier refs.
func authorCloudEvents() agenteval.Task {
	const contribDir = "contrib/cloudevents/sdk-go.v2"
	// Globbed rather than fixed, so a run that puts the package one directory
	// over still gets its conventions scored instead of scoring nothing.
	const goFiles = "contrib/cloudevents/*/*.go"
	return agenteval.Task{
		Spec: agenteval.TaskSpec{
			TaskID:         "author-cloudevents-v2",
			ExperimentName: "author-integration-cloudevents-v2",
			Mutation: agenteval.Mutation{
				Kind: agenteval.MutationDeletePaths,
				Paths: []string{
					contribDir + "/carriers.go",
					contribDir + "/cloudevents.go",
					contribDir + "/option.go",
					contribDir + "/orchestrion.yml",
				},
				AllowMissing: true,
			},
			ValidationCommands: RepositoryChecks(),
			ExpectedChangedPaths: []string{
				contribDir + "/cloudevents.go",
				contribDir + "/cloudevents_test.go",
				contribDir + "/example_test.go",
				contribDir + "/go.mod",
				contribDir + "/go.sum",
				contribDir + "/option.go",
				contribDir + "/orchestrion.yml",
				"ddtrace/tracer/option.go",
				"go.work",
				"go.work.sum",
				"instrumentation/packages.go",
				"internal/env/supported_configurations.gen.go",
				"internal/env/supported_configurations.json",
				"internal/orchestrion/_integration/cloudevents-sdk-go.v2/cloudevents.go",
				"internal/orchestrion/_integration/cloudevents-sdk-go.v2/generated_test.go",
				"internal/orchestrion/_integration/go.mod",
				"internal/orchestrion/_integration/go.sum",
				"internal/stacktrace/contribs_generated.go",
				"orchestrion/all/go.mod",
				"orchestrion/all/go.sum",
				"orchestrion/all/orchestrion.tool.go",
			},
			// Narrower than CommonForbidden on purpose. A new contrib is its own
			// module, while the repository's root module files must stay unchanged.
			ForbiddenPaths:   []string{".github/", ".gitlab/", "go.mod", "go.sum"},
			MaxDiffLines:     2000,
			DocsExpectedRead: []string{"contrib/INTEGRATIONS.md", "contrib/ORCHESTRION.md", "contrib/AGENTS.md"},
			OrchestrionYAML:  contribDir + "/orchestrion.yml",
			UpstreamMarkers:  upstreamMarkers,
			CheckWeights: map[string]float64{
				agenteval.CheckDiffNotEmpty:            3,
				agenteval.CheckExpectedPathsTouched:    3,
				agenteval.CheckForbiddenPathsUntouched: 3,
				agenteval.CheckDiffWithinLimit:         1,
				agenteval.CheckOrchestrionAspect:       3,
				agenteval.CheckOrchestrionSchemaValid:  3,
				"registered_in_packages_go":            3,
				"module_registered_in_workspace":       3,
				"orchestrion_integration_test":         3,
				"option_iface":                         2,
				"option_fn":                            2,
				"custom_tag_option":                    2,
				"service_source_recorded":              2,
				"service_source_option_override":       2,
				"semconv_event_tags":                   2,
				"package_example":                      2,
				"no_legacy_op_name_helper":             1,
				"no_legacy_naming_map":                 1,
				"no_adhoc_event_tags":                  1,
			},
			SourceChecks: []agenteval.SourceCheck{
				Present("registered_in_packages_go", `github\.com/cloudevents/sdk-go/v2`,
					"instrumentation/packages.go"),
				Present("module_registered_in_workspace", regexpQuote("./"+contribDir), "go.work"),
				Present("option_iface", `(?m)^type Option interface\b`, goFiles),
				Present("option_fn", `(?m)^type OptionFn\b`, goFiles),
				Present("custom_tag_option", `func WithCustomTag\(`, goFiles),
				Present("service_source_recorded", `ServiceNameWithSource\(`, goFiles),
				Present("service_source_option_override", `ServiceSourceWithServiceOption`, goFiles),
				// INTEGRATIONS.md is explicit that new integrations hardcode the
				// operation name. instr.ServiceName stays allowed: it is how the
				// default service name is meant to be resolved.
				Absent("no_legacy_op_name_helper", `instr\.OperationName\(`, goFiles),
				// Windowed rather than brace-matched: the entry is a few hundred
				// bytes, so a naming key within that distance of the package
				// constant belongs to it.
				Absent("no_legacy_naming_map",
					`PackageCloudEvents[\s\S]{0,400}?(naming:|buildOpName|buildServiceName)`,
					"instrumentation/packages.go"),
				Present("semconv_event_tags", `cloudevents\.event_(id|type|source)`, goFiles),
				Absent("no_adhoc_event_tags",
					`"cloudevents\.(id|type|source|specversion|spec_version|subject)"`, goFiles),
				Present("package_example", `(?m)^func Example\(\)`, "contrib/cloudevents/*/*_test.go"),
				Exists("orchestrion_integration_test",
					"internal/orchestrion/_integration/*cloudevents*/*.go"),
			},
		},
		Prompts: []agenteval.PromptVariant{
			{ID: "add-support", Prompt: "Add support for github.com/cloudevents/sdk-go/v2 in dd-trace-go."},
			{ID: "new-integration", Prompt: "Create a new integration for github.com/cloudevents/sdk-go/v2."},
			{
				ID: "follow-repo-guidance",
				Prompt: "Create a new integration for github.com/cloudevents/sdk-go/v2. " +
					"Find any repository guidance for writing integrations and follow every step and convention rigorously.",
			},
		},
		Metadata: agenteval.TaskMetadata{
			Category:    "integration_authoring",
			FailureMode: "new_integration_conventions",
			Size:        agenteval.SizeLarge,
			Source:      "https://github.com/DataDog/dd-trace-go/pull/5156",
		},
	}
}
