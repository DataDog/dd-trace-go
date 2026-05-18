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

// SecurityTestingHeaderTagsOption tags security-testing headers from the request.
func SecurityTestingHeaderTagsOption(headers http.Header) tracer.StartSpanOption {
	return func(cfg *tracer.StartSpanConfig) {
		if cfg.Tags == nil {
			cfg.Tags = make(map[string]any)
		}
		listenerhttpsec.SetSecurityTestingHeaderTags(cfg.Tags, headers)
	}
}

// SecurityTestingHeaderTagsFromBytesOption tags security-testing headers from byte lookup results.
func SecurityTestingHeaderTagsFromBytesOption(values func(string) [][]byte) tracer.StartSpanOption {
	return func(cfg *tracer.StartSpanConfig) {
		if cfg.Tags == nil {
			cfg.Tags = make(map[string]any)
		}
		listenerhttpsec.SetSecurityTestingHeaderTagsFromBytes(cfg.Tags, values)
	}
}
