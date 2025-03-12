// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"net/http"
)

// WithResourceNamer populates the name of a resource based on a custom function.
func WithResourceNamer(namer func(req *http.Request) string) Option {
	return func(cfg *Config) {
		cfg.ResourceNamer = namer
	}
}
