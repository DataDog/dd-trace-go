// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"time"
)

type StartSpanConfig struct {
	SessionID     string
	ModelName     string
	ModelProvider string
	MLApp         string
	StartTime     time.Time
}

type FinishSpanConfig struct {
	FinishTime time.Time
	Error      error
}
