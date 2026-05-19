// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	listenerhttpsec "github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/httpsec"
)

// SetSecurityTestingHeaderTags tags security-testing headers from the request.
func SetSecurityTestingHeaderTags(tags map[string]any, headers http.Header) {
	tagNames, tagValues, count := listenerhttpsec.SecurityTestingHeaderTagValues(headers)
	for i := range count {
		tags[tagNames[i]] = tagValues[i]
	}
}

// AppendSecurityTestingHeaderTags appends tag options for present security-testing headers.
func AppendSecurityTestingHeaderTags(opts []tracer.StartSpanOption, headers http.Header) []tracer.StartSpanOption {
	tagNames, tagValues, count := listenerhttpsec.SecurityTestingHeaderTagValues(headers)
	return appendSecurityTestingHeaderTags(opts, tagNames, tagValues, count)
}

// AppendSecurityTestingHeaderTagsFromBytes appends tag options for present byte header lookups.
func AppendSecurityTestingHeaderTagsFromBytes(opts []tracer.StartSpanOption, values func(string) [][]byte) []tracer.StartSpanOption {
	tagNames, tagValues, count := listenerhttpsec.SecurityTestingHeaderByteTagValues(values)
	return appendSecurityTestingHeaderTags(opts, tagNames, tagValues, count)
}

func appendSecurityTestingHeaderTags(opts []tracer.StartSpanOption, tagNames, tagValues [2]string, count int) []tracer.StartSpanOption {
	for i := range count {
		opts = append(opts, tracer.Tag(tagNames[i], tagValues[i]))
	}
	return opts
}
