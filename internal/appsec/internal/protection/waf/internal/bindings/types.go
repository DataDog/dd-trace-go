// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package bindings

// Action the WAF returns after running it.
type Action int

const (
	// NoAction is returned when no values matched the rule.
	NoAction Action = iota
	// MonitorAction is returned when one or several values matched the WAF rule and an event should be logged.
	MonitorAction
	// BlockAction is returned when one or several values matched the WAF rule, an event should be logged and the
	// calling operation should be blocked.
	BlockAction
)
