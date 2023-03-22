// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema

type VersionOverrideFunc func() string

type Option func(cfg *config)

type config struct {
	versionOverrides map[Version]VersionOverrideFunc
}

func WithVersionOverride(v Version, f VersionOverrideFunc) Option {
	return func(cfg *config) {
		if cfg.versionOverrides == nil {
			cfg.versionOverrides = make(map[Version]VersionOverrideFunc)
		}
		cfg.versionOverrides[v] = f
	}
}
