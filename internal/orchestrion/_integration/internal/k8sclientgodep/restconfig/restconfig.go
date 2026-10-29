// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package restconfig builds a client-go config inside a dependency module.
package restconfig

import (
	"k8s.io/client-go/rest"
)

// New returns a config for the API server at host.
func New(host string) *rest.Config {
	return &rest.Config{Host: host}
}
