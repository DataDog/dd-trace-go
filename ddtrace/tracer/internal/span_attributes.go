// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

// TagValue holds a tag string and a flag indicating whether it was explicitly set.
// The zero value represents an absent tag; TagOf("") represents an explicit empty string.
type TagValue struct {
	v   string
	set bool
}

// TagOf returns a TagValue that is marked as explicitly set.
func TagOf(v string) TagValue { return TagValue{v: v, set: true} }

// Val returns the string value, ignoring whether it was set.
func (t TagValue) Val() string { return t.v }

// Get returns the string value and whether it was explicitly set,
// mirroring the two-value map lookup idiom.
func (t TagValue) Get() (string, bool) { return t.v, t.set }

// SpanAttributes holds the four V1-protocol promoted span fields.
// Grouping them prevents accidental field-level initialization in struct literals
// and makes their shared encoding semantics explicit.
// The zero value of each TagValue means "never set".
type SpanAttributes struct {
	Env       TagValue // ext.Environment
	Version   TagValue // ext.Version
	Component TagValue // ext.Component
	SpanKind  TagValue // ext.SpanKind
}
