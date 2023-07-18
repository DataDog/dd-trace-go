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

// headerTagRegexp is used to replace all invalid characters in the config. Only alphanumerics, whitespaces and dashes allowed.
var headerTagRegexp = regexp.MustCompile("[^a-zA-Z0-9 -]")

// NormalizeHeaderTag accepts a string that contains a header and an optional mapped tag key,
// e.g, "header" or "header:tag" where `tag` will be the name of the header tag.
// If multiple colons exist in the input, it splits on the last colon.
// e.g, "first:second:third" gets split into `header = "first:second"` and `tag="third"`
func NormalizeHeaderTag(headerAsTag string) (header string, tag string) {
	header = strings.ToLower(strings.TrimSpace(headerAsTag))
	// if a colon is found in `headerAsTag`
	if last := strings.LastIndex(header, ":"); last >= 0 {
		header, tag = header[:last], header[last+1:]
		header, tag = strings.TrimSpace(header), strings.TrimSpace(tag)
	} else {
		tag = ext.HTTPRequestHeaders + "." + headerTagRegexp.ReplaceAllString(header, "_")
	}
	return header, tag
}
