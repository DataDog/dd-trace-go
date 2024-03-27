// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package globalconfig stores configuration which applies globally to both the tracer
// and integrations.
// Note that this package is for dd-trace-go.v1 internal testing utilities only.
// This package is not intended for use by external consumers, no API stability is guaranteed.
package globalconfig

import (
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
)

// AnalyticsRate returns the sampling rate at which events should be marked. It uses
// synchronizing mechanisms, meaning that for optimal performance it's best to read it
// once and store it.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func AnalyticsRate() float64 {
	return globalconfig.AnalyticsRate()
}

// SetAnalyticsRate sets the given event sampling rate globally.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func SetAnalyticsRate(rate float64) {
	globalconfig.SetAnalyticsRate(rate)
}

// ServiceName returns the default service name used by non-client integrations such as servers and frameworks.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func ServiceName() string {
	return globalconfig.ServiceName()
}

// SetServiceName sets the global service name set for this application.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func SetServiceName(name string) {
	globalconfig.SetServiceName(name)
}

// RuntimeID returns this process's unique runtime id.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func RuntimeID() string {
	return globalconfig.RuntimeID()
}

// HeaderTagMap returns the mappings of headers to their tag values.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func HeaderTagMap() *internal.LockMap {
	return globalconfig.HeaderTagMap()
}

// HeaderTag returns the configured tag for a given header.
// This function exists for testing purposes, for performance you may want to use `HeaderTagMap`.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func HeaderTag(header string) string {
	return globalconfig.HeaderTag(header)
}

// SetHeaderTag adds config for header `from` with tag value `to`.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func SetHeaderTag(from, to string) {
	globalconfig.SetHeaderTag(from, to)
}

// HeaderTagsLen returns the length of globalconfig's headersAsTags map, 0 for empty map.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func HeaderTagsLen() int {
	return globalconfig.HeaderTagsLen()
}

// ClearHeaderTags assigns headersAsTags to a new, empty map.
// It is invoked when WithHeaderTags is called, in order to overwrite the config.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func ClearHeaderTags() {
	globalconfig.ClearHeaderTags()
}
