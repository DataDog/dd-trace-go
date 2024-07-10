// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
)

// ciVisibilityCloseAction defines an action to be executed when CI visibility is closing.
type ciVisibilityCloseAction func()

var (
	// ciVisibilityInitializationOnce ensures we initialize the CI visibility tracer only once.
	ciVisibilityInitializationOnce sync.Once

	// closeActions holds CI visibility close actions.
	closeActions []ciVisibilityCloseAction

	// closeActionsMutex synchronizes access to closeActions.
	closeActionsMutex sync.Mutex

	// mTracer contains the mock tracer instance for testing purposes
	mTracer mocktracer.Tracer
)

// EnsureCiVisibilityInitialization initializes the CI visibility tracer if it hasn't been initialized already.
func EnsureCiVisibilityInitialization() {
	internalCiVisibilityInitialization(func(opts []tracer.StartOption) {
		// Initialize the tracer.
		tracer.Start(opts...)
	})
}

// InitializeCIVisibilityMock initialize the mocktracer for CI Visibility usage
func InitializeCIVisibilityMock() mocktracer.Tracer {
	internalCiVisibilityInitialization(func([]tracer.StartOption) {
		// Initialize the mocktracer
		mTracer = mocktracer.Start()
	})
	return mTracer
}

func internalCiVisibilityInitialization(tracerInitializer func([]tracer.StartOption)) {
	ciVisibilityInitializationOnce.Do(func() {
		// Since calling this method indicates we are in CI Visibility mode, set the environment variable.
		_ = os.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")

		// Avoid sampling rate warning (in CI Visibility mode we send all data)
		_ = os.Setenv("DD_TRACE_SAMPLE_RATE", "1")

		// Preload the CodeOwner file
		_ = utils.GetCodeOwners()

		// Preload all CI, Git, and CodeOwners tags.
		ciTags := utils.GetCITags()

		// Check if DD_SERVICE has been set; otherwise default to the repo name (from the spec).
		var opts []tracer.StartOption
		if v := os.Getenv("DD_SERVICE"); v == "" {
			if repoURL, ok := ciTags[constants.GitRepositoryURL]; ok {
				// regex to sanitize the repository url to be used as a service name
				repoRegex := regexp.MustCompile(`(?m)/([a-zA-Z0-9\\\-_.]*)$`)
				matches := repoRegex.FindStringSubmatch(repoURL)
				if len(matches) > 1 {
					repoURL = strings.TrimSuffix(matches[1], ".git")
				}
				opts = append(opts, tracer.WithService(repoURL))
			}
		}

		// Initialize the tracer
		tracerInitializer(opts)

		// Handle SIGINT and SIGTERM signals to ensure we close all open spans and flush the tracer before exiting
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-signals
			ExitCiVisibility()
			os.Exit(1)
		}()
	})
}

// PushCiVisibilityCloseAction adds a close action to be executed when CI visibility exits.
func PushCiVisibilityCloseAction(action ciVisibilityCloseAction) {
	closeActionsMutex.Lock()
	defer closeActionsMutex.Unlock()
	closeActions = append([]ciVisibilityCloseAction{action}, closeActions...)
}

// ExitCiVisibility executes all registered close actions and stops the tracer.
func ExitCiVisibility() {
	closeActionsMutex.Lock()
	defer closeActionsMutex.Unlock()
	defer func() {
		closeActions = []ciVisibilityCloseAction{}

		tracer.Flush()
		tracer.Stop()
	}()
	for _, v := range closeActions {
		v()
	}
}
