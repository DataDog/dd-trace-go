// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package addresses

import (
	"github.com/DataDog/go-libddwaf/v5/timer"
)

// Scope divides the time spent in go-libddwaf between multiple parts before it is passed to libddwaf as a TimerKey.
type Scope = timer.Key

const (
	RASPScope Scope = "rasp"
	WAFScope  Scope = "waf"
)

var Scopes = [...]Scope{
	RASPScope,
	WAFScope,
}
