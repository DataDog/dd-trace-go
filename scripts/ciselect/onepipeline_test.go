// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	rootPipelinePath = ".gitlab-ci.yml"
	onePipelinePath  = ".gitlab/one-pipeline.locked.yml"
)

// skippedSharedJobs need packaging jobs that SKIP_SHARED_PIPELINE removes, so
// GitLab refuses to create a pipeline that includes any of them.
var skippedSharedJobs = []string{
	"promote-oci-to-prod",
	"promote-oci-to-staging",
	"publish_dora_metrics",
}

func TestSkippedSharedPipelineDisablesPromoteJobs(t *testing.T) {
	root, err := repoRoot(".")
	if err != nil {
		t.Fatalf("repoRoot() = %v", err)
	}

	var pipeline struct {
		Variables map[string]any `yaml:"variables"`
	}
	if err := yaml.Unmarshal([]byte(readText(t, root, rootPipelinePath)), &pipeline); err != nil {
		t.Fatalf("parse %s: %v", rootPipelinePath, err)
	}
	if pipeline.Variables["SKIP_SHARED_PIPELINE"] != "true" {
		t.Skipf("%s no longer sets SKIP_SHARED_PIPELINE; the promote jobs may run as the template intends",
			rootPipelinePath)
	}

	var locked map[string]any
	if err := yaml.Unmarshal([]byte(readText(t, root, onePipelinePath)), &locked); err != nil {
		t.Fatalf("parse %s: %v", onePipelinePath, err)
	}
	for _, name := range skippedSharedJobs {
		job, ok := locked[name].(map[string]any)
		if !ok {
			t.Errorf("%s must override %s with `rules: [{when: never}]`", onePipelinePath, name)
			continue
		}
		rules, _ := job["rules"].([]any)
		var rule map[string]any
		if len(rules) == 1 {
			rule, _ = rules[0].(map[string]any)
		}
		if len(rule) != 1 || rule["when"] != "never" {
			t.Errorf("%s: %s rules = %v, want exactly [{when: never}]", onePipelinePath, name, job["rules"])
		}
	}
}
