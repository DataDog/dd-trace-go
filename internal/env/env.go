// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package env

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

var privateCIVisibilityRetryProcessEnvironment = struct {
	active bool
	values map[string]string
	err    error
}{}

func init() {
	child, ok := os.LookupEnv("DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_CHILD")
	enabled, err := strconv.ParseBool(child)
	if !ok || err != nil || !enabled {
		return
	}
	privateCIVisibilityRetryProcessEnvironment.active = true
	privateCIVisibilityRetryProcessEnvironment.values = make(map[string]string, 5)
	for _, key := range privateCIVisibilityRetryProcessKeys() {
		if value, ok := os.LookupEnv(key); ok {
			privateCIVisibilityRetryProcessEnvironment.values[key] = value
		}
		if err := os.Unsetenv(key); err != nil && privateCIVisibilityRetryProcessEnvironment.err == nil {
			privateCIVisibilityRetryProcessEnvironment.err = err
		}
	}
}

// Get is a wrapper around env.Get that validates the environment variable
// against a list of supported environment variables.
//
// If the environment variable has aliases, the function will also check the aliases
// and return the value of the first alias that is set.
//
// When a environment variable is not supported because it is not
// listed in the list of supported environment variables, the function will log an error
// and behave as if the environment variable was not set.
//
// In testing mode, the reader will automatically add the environment variable
// to the configuration file.
func Get(name string) string {
	if !verifySupportedConfiguration(name) {
		return ""
	}

	if v := os.Getenv(name); v != "" {
		return v
	}

	for _, alias := range KeyAliases[name] {
		if v := os.Getenv(alias); v != "" {
			return v
		}
	}

	return ""
}

// Lookup is a wrapper around os.LookupEnv that validates the environment variable
// against a list of supported environment variables.
//
// If the environment variable has aliases, the function will also check the aliases.
// and return the value of the first alias that is set.
//
// When a environment variable is not supported because it is not
// listed in the list of supported environment variables, the function will log an error
// and behave as if the environment variable was not set.
//
// In testing mode, the reader will automatically add the environment variable
// to the configuration file.
func Lookup(name string) (string, bool) {
	if !verifySupportedConfiguration(name) {
		return "", false
	}

	if v, ok := os.LookupEnv(name); ok {
		return v, true
	}

	for _, alias := range KeyAliases[name] {
		if v, ok := os.LookupEnv(alias); ok {
			return v, true
		}
	}

	return "", false
}

// LookupPrivate returns a raw environment variable value for package-private
// CI Visibility retry-process transport keys that must not be added to
// supported configurations.
func LookupPrivate(name string) (string, bool) {
	if !isPrivateCIVisibilityRetryProcessKey(name) {
		return "", false
	}
	if privateCIVisibilityRetryProcessEnvironment.active {
		value, ok := privateCIVisibilityRetryProcessEnvironment.values[name]
		return value, ok
	}
	return os.LookupEnv(name)
}

// PrivateRetryProcessTransportError returns an error encountered while removing
// child transport keys from the process environment during package startup.
func PrivateRetryProcessTransportError() error {
	return privateCIVisibilityRetryProcessEnvironment.err
}

func isPrivateCIVisibilityRetryProcessKey(name string) bool {
	switch name {
	case "DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_CHILD",
		"DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_RESULT_PATH",
		"DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_TEST_NAME",
		"DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_ATTEMPT",
		"DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_REASON":
		return true
	default:
		return false
	}
}

func privateCIVisibilityRetryProcessKeys() [5]string {
	return [5]string{
		"DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_CHILD",
		"DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_RESULT_PATH",
		"DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_TEST_NAME",
		"DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_ATTEMPT",
		"DD_CIVISIBILITY_INTERNAL_RETRY_PROCESS_REASON",
	}
}

func verifySupportedConfiguration(name string) bool {
	if strings.HasPrefix(name, "DD_") || strings.HasPrefix(name, "OTEL_") {
		if _, ok := SupportedConfigurations[name]; !ok {
			if testing.Testing() {
				addSupportedConfigurationToFile(name)
			}

			log.Error("config: usage of a unlisted environment variable: %s", name)

			return false
		}
	}

	return true
}
