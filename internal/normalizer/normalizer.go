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

var headerTagRegexp = regexp.MustCompile("[^a-zA-Z0-9 -]")

// NormalizeHeaderTag accepts a string that contains a header and an optional mapped tag key,
// e.g, "header" or "header:tag" where `tag` will be the name of the header tag.
// It will split on the last colon, e.g, passing input "first:second:third" will result in 
// `header` value `"first:second"` and `tag` value `"third`
// func NormalizeHeaderTag(headerAsTag string) (header string, tag string) {
// 	lastIdx := strings.LastIndex(headerAsTag, ":")

// 	// If no colon or colon sits at the very beginning or very end of the string
// 	if lastIdx == -1 || lastIdx == 0 || lastIdx == len(headerAsTag)-1 {
// 		headerAsTag = strings.ToLower(strings.TrimSpace(headerAsTag))
// 		return headerAsTag, ext.HTTPRequestHeaders + "." + headerTagRegexp.ReplaceAllString(headerAsTag, "_")
// 	}
// 	return strings.ToLower(strings.TrimSpace(headerAsTag[:lastIdx])), strings.ToLower(strings.TrimSpace(headerAsTag[lastIdx+1:]))
// }

func NormalizeHeaderTag(headerAsTag string) (header string, tag string) {
	// TODO: mtoffl01 - check how other tracing libraries handle casing for the tag
	header = strings.ToLower(strings.TrimSpace(headerAsTag))
	// If colon was found, and it is neither the first nor last character in the string, split the tag from the last colon
	if last := strings.LastIndex(header, ":"); last >= 0 {
		// TODO: mtoffl01 - check how other libraries handle colon as leading and trailing characters e.g, ":header" and "header:"
		header, tag = header[:last], header[last+1:]
		// normalize the header to all lowercase, but leave the tag as it was specified
		header, tag = strings.TrimSpace(header), strings.TrimSpace(tag)
	} else {
		tag = ext.HTTPRequestHeaders + "." + headerTagRegexp.ReplaceAllString(header, "_")
	}
	return header, tag
}
