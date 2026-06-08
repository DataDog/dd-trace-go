// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
)

// addRuntimeIDToGlobalTags is the apply callback for globalTags. The runtime ID
// must appear on every span (via the global tags applied at span start), so it
// is (re)added whenever the tag set changes — in particular after a Remote
// Config update replaces the whole map, which would otherwise drop it.
func addRuntimeIDToGlobalTags(tags map[string]any) bool {
	if tags == nil {
		return false
	}
	tags[ext.RuntimeID] = globalconfig.RuntimeID()
	return true
}
