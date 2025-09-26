// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package dataset

type createConfig struct {
	description           string
	csvDelimiter          rune
	csvMetadataCols       []string
	csvExpectedOutputCols []string
}

func defaultCreateConfig() *createConfig {
	return &createConfig{
		description:           "",
		csvDelimiter:          ',',
		csvMetadataCols:       nil,
		csvExpectedOutputCols: nil,
	}
}

type CreateOption func(cfg *createConfig)

func WithDescription(description string) CreateOption {
	return func(cfg *createConfig) {
		cfg.description = description
	}
}

func WithCSVDelimiter(delimiter rune) CreateOption {
	return func(cfg *createConfig) {
		cfg.csvDelimiter = delimiter
	}
}

func WithCSVMetadataColumns(cols []string) CreateOption {
	return func(cfg *createConfig) {
		cfg.csvMetadataCols = cols
	}
}

func WithCSVExpectedOutputColumns(cols []string) CreateOption {
	return func(cfg *createConfig) {
		cfg.csvExpectedOutputCols = cols
	}
}
