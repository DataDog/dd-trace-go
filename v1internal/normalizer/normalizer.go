// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package normalizer provides tag normalization
// This package is not intended for use by external consumers, no API stability is guaranteed.
package normalizer

import (
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"
)

// HeaderTag accepts a string that contains a header and an optional mapped tag key,
// e.g, "header" or "header:tag" where `tag` will be the name of the header tag.
// If multiple colons exist in the input, it splits on the last colon.
// e.g, "first:second:third" gets split into `header = "first:second"` and `tag="third"`
// The returned header is in canonical MIMEHeader format.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func HeaderTag(headerAsTag string) (header string, tag string) {
	return normalizer.HeaderTag(headerAsTag)
}

// HeaderTagSlice accepts a slice of strings that contain headers and optional mapped tag key.
// Headers beginning with "x-datadog-" are ignored.
// See HeaderTag for details on formatting.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func HeaderTagSlice(headers []string) map[string]string {
	return normalizer.HeaderTagSlice(headers)
}
