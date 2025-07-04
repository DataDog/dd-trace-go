// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package env

import (
	"os"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// GetEnv is a wrapper around os.GetEnv that validates the environment variable
// against a list of supported environment variables.
//
// When a environment variable is not supported because it is not
// listed in the list of supported environment variables, the function will log an error
// and behave as if the environment variable was not set.
//
// In testing mode, the reader will automatically add the environment variable
// to the configuration file.
func GetEnv(name string) string {
	if !verifySupportedConfiguration(name) {
		return ""
	}

	return os.Getenv(name)
}

// LookupEnv is a wrapper around os.LookupEnv that validates the environment variable
// against a list of supported environment variables.
//
// When a environment variable is not supported because it is not
// listed in the list of supported environment variables, the function will log an error
// and behave as if the environment variable was not set.
//
// In testing mode, the reader will automatically add the environment variable
// to the configuration file.
func LookupEnv(name string) (string, bool) {
	if !verifySupportedConfiguration(name) {
		return "", false
	}

	return os.LookupEnv(name)
}

func verifySupportedConfiguration(name string) bool {
	if strings.HasPrefix(name, "DD_") || strings.HasPrefix(name, "OTEL_") {
		if _, ok := SupportedConfigurations[name]; !ok {
			if testing.Testing() {
				// TODO: add value to supported configurations
				// TODO: git status supported-configurations.json in CI
			}

			log.Error("config: usage of a unlisted environment variable: %s", name)

			return false
		}
	}

	return true
}
