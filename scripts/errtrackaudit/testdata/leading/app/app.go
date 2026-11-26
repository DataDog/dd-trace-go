// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import "example.com/leading/internal/log"

func A() {
	/*errtrack:ignore*/ log.Error("leading single")
	/*errtrack:ignore*/ log.Error(
		"leading multi",
	)
	log.Error( //errtrack:ignore trailing on the first line
		"trailing multi",
	)
	log.Error(
		"inside multi", //errtrack:ignore inside the span
	)
	log.Error("plain")
}
