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

// ConvertHeaderToTag accepts a string that contains a header and an optional mapped tag key,
// e.g, "header" or "header:tag" where `tag` will be the name of the header tag.
func ConvertHeaderToTag(headerAsTag string) (header string, tag string) {
	headerAsTag = strings.ToLower(strings.TrimSpace(headerAsTag))
	lastIdx := strings.LastIndex(headerAsTag, ":")
	// If no colon or colon sits at the very beginning or very end of the string
	if lastIdx == -1 || lastIdx == 0 || lastIdx == len(headerAsTag)-1 {
		return headerAsTag, ext.HTTPRequestHeaders + "." + normalizeTag(headerAsTag)
	}
	return headerAsTag[:lastIdx], headerAsTag[lastIdx+1:]
}

// normalizeTag removes all "." in the string with "_" and returns the result
func normalizeTag(header string) (tag string) {
	regex := regexp.MustCompile(`[^a-zA-Z0-9 -]+`)
	return regex.ReplaceAllString(header, "_")
}