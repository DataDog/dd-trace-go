// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package env

import (
	"fmt"
	"os"
	"testing"
)

func init() {
	var err error
	reader, err = newConfigurationInversionReader()
	if err != nil {
		panic(fmt.Errorf("failed to initialize configuration inversion reader: %w", err))
	}
}

var reader *configurationInversionReader

// GetEnv is a wrapper around os.GetEnv that validates the environment variable
// against a configuration file listing every known environment variable.
//
// When a environment variable is not supported because it is not
// listed in the configuration file, the reader will log an error
// and behave as if the environment variable was not set.
//
// In testing mode, the reader will automatically add the environment variable
// to the configuration file.
func GetEnv(name string) string {
	return reader.getEnv(name)
}

// LookupEnv is a wrapper around os.LookupEnv that validates the environment variable
// against a configuration file listing every known environment variable.
//
// When a environment variable is not supported because it is not
// listed in the configuration file, the reader will log an error
// and behave as if the environment variable was not set.
//
// In testing mode, the reader will automatically add the environment variable
// to the configuration file.
func LookupEnv(name string) (string, bool) {
	return reader.lookupEnv(name)
}

const (
	defaultSupportedConfigurationPath = "./supported-configurations.json"
)

func newConfigurationInversionReader() (*configurationInversionReader, error) {
	return &configurationInversionReader{}, nil
}

// configurationInversionReader is a wrapper used to read the environment variables
// and validate their usage against a configuration file listing every
// known environment variable.
//
// This allows us to have a single point of truth for the environment variables
// and ensure that we are not using any environment variable that is not
// explicitly listed in the configuration file.
type configurationInversionReader struct {
}

func (e *configurationInversionReader) getEnv(name string) string {
	if testing.Testing() {
		// error log
		// add value to supported configurations
		// git status supported-configurations.json
	}

	return os.Getenv(name)
}

func (e *configurationInversionReader) lookupEnv(name string) (string, bool) {
	return os.LookupEnv(name)
}
