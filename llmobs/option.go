// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"github.com/DataDog/dd-trace-go/v2/llmobs/internal"
)

type Option = internal.Option

var WithHTTPClient = internal.WithHTTPClient
