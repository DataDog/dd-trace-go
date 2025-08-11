// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Use the `child_pipeline.yml.tpl` file to create the parallel matrix with which our
// benchmarks on new PRs will be run.

package main

import (
	"log"
	"os"
	"strings"
	"text/template"
)

// Config holds the data to be injected into the template.
type Config struct {
	ProjectPath    string
	CommitSHA      string
	BenchmarkNames []string
}

func main() {
	// Set up environment variables provided by our GitLab CI runners.
	// BENCHMARK_TARGETS is defined in .gitlab-ci.yml
	projectPath := os.Getenv("CI_PROJECT_PATH")
	commitSHA := os.Getenv("CI_COMMIT_SHA")
	benchmarkTargets := os.Getenv("BENCHMARK_TARGETS")

	if projectPath == "" || commitSHA == "" || benchmarkTargets == "" {
		log.Fatal("Required environment variables CI_PROJECT_PATH, CI_COMMIT_SHA, or BENCHMARK_TARGETS are not set.")
	}

	// BENCHMARK_TARGETS is defined as a string of benchmark names separated by a "|"
	// In order to run our benchmarks in parallel, we want to have our benchmarks as a string list
	config := Config{
		ProjectPath:    projectPath,
		CommitSHA:      commitSHA,
		BenchmarkNames: strings.Split(benchmarkTargets, "|"),
	}

	tmpl, err := template.ParseFiles("child_pipeline.yml.tpl")
	if err != nil {
		log.Fatalf("Error parsing template: %s", err.Error())
	}

	outputFile, err := os.Create("generated_benchmark_matrix.yml")
	if err != nil {
		log.Fatalf("Error creating output file: %s", err.Error())
	}
	defer outputFile.Close()

	err = tmpl.Execute(outputFile, config)
	if err != nil {
		log.Fatalf("Error executing template: %s", err.Error())
	}

	log.Println("Successfully generated benchmarking matrix template.")
}
