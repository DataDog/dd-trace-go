// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !appsec
// +build !appsec

package appsec

import "gopkg.in/DataDog/dd-trace-go.v1/internal/log"

// Status returns the AppSec status string: "enabled" when both the appsec
// build tag is enabled and the env var DD_APPSEC_ENABLED is set to true, or
// "disabled" otherwise.
func Status() string {
	return "disabled"
}

// Start AppSec when enabled is enabled by both using the appsec build tag and
// setting the environment variable DD_APPSEC_ENABLED to true.
func Start() {
	if enabled, err := isEnabled(); err != nil {
		// Something went wrong while checking the DD_APPSEC_ENABLED configuration
		log.Error("appsec: error while checking if appsec is enabled: %v", err)
	} else if enabled {
		// The user is willing to enabled appsec but didn't have the build tag
		log.Info("appsec: enabled by the configuration but has not been activated during the compilation: please add the go build tag `appsec` to your build options to enable it")
	} else {
		// The user is not willing to start appsec, a simple debug log is enough
		log.Debug("appsec: not been not enabled during the compilation: please add the go build tag `appsec` to your build options to enable it")
	}
}

// Stop AppSec.
func Stop() {}
