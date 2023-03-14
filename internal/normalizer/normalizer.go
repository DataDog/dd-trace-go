// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package normalizer provides tag normalization
package normalizer

import (
	"regexp"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

var headerTagRegexp = regexp.MustCompile("[^a-zA-Z0-9 -]+")

// NormalizeHeaderTag accepts a string that contains a header and an optional mapped tag key,
// e.g, "header" or "header:tag" where `tag` will be the name of the header tag.
func NormalizeHeaderTag(headerAsTag string) (header string, tag string) {
	lastIdx := strings.LastIndex(headerAsTag, ":")

	// If no colon or colon sits at the very beginning or very end of the string
	if lastIdx == -1 || lastIdx == 0 || lastIdx == len(headerAsTag)-1 {
		headerAsTag = strings.ToLower(strings.TrimSpace(headerAsTag))
		return headerAsTag, ext.HTTPRequestHeaders + "." + headerTagRegexp.ReplaceAllString(headerAsTag, "_")
	}
	return strings.ToLower(strings.TrimSpace(headerAsTag[:lastIdx])), strings.ToLower(strings.TrimSpace(headerAsTag[lastIdx+1:]))
}
