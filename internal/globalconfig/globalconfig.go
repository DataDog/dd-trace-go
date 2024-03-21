// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package globalconfig stores configuration which applies globally to both the tracer
// and integrations.
package globalconfig

import (
	v2 "github.com/DataDog/dd-trace-go/v2/v1internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

// AnalyticsRate returns the sampling rate at which events should be marked. It uses
// synchronizing mechanisms, meaning that for optimal performance it's best to read it
// once and store it.
func AnalyticsRate() float64 {
	return v2.AnalyticsRate()
}

// SetAnalyticsRate sets the given event sampling rate globally.
func SetAnalyticsRate(rate float64) {
	v2.SetAnalyticsRate(rate)
}

// ServiceName returns the default service name used by non-client integrations such as servers and frameworks.
func ServiceName() string {
	return v2.ServiceName()
}

// SetServiceName sets the global service name set for this application.
func SetServiceName(name string) {
	v2.SetServiceName(name)
}

// RuntimeID returns this process's unique runtime id.
func RuntimeID() string {
	return v2.RuntimeID()
}

// HeaderTagMap returns the mappings of headers to their tag values
func HeaderTagMap() *internal.LockMap {
	tm := v2.HeaderTagMap()
	m := internal.NewLockMap(make(map[string]string, tm.Len()))

	tm.Iter(func(k string, v string) {
		m.Set(k, v)
	})

	return m
}

// HeaderTag returns the configured tag for a given header.
// This function exists for testing purposes, for performance you may want to use `HeaderTagMap`
func HeaderTag(header string) string {
	return v2.HeaderTag(header)
}

// SetHeaderTag adds config for header `from` with tag value `to`
func SetHeaderTag(from, to string) {
	v2.SetHeaderTag(from, to)
}

// HeaderTagsLen returns the length of globalconfig's headersAsTags map, 0 for empty map
func HeaderTagsLen() int {
	return v2.HeaderTagsLen()
}

// ClearHeaderTags assigns headersAsTags to a new, empty map
// It is invoked when WithHeaderTags is called, in order to overwrite the config
func ClearHeaderTags() {
	v2.ClearHeaderTags()
}
