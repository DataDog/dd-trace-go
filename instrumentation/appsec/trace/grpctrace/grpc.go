// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpctrace

import (
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace/httptrace"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// SetSecurityEventsTags sets the AppSec events span tags.
func SetSecurityEventsTags(span trace.TagSetter, events []any) {
	if err := setSecurityEventsTags(span, events); err != nil {
		log.Error("appsec: unexpected error while creating the appsec events tags: %v", err)
	}
}

func setSecurityEventsTags(span trace.TagSetter, events []any) error {
	if events == nil {
		return nil
	}
	return trace.SetEventSpanTags(span, events)
}

// SetRequestMetadataTags sets the gRPC request metadata span tags.
func SetRequestMetadataTags(span trace.TagSetter, md map[string][]string) {
	for h, v := range httptrace.NormalizeHTTPHeaders(md) {
		span.SetTag("grpc.metadata."+h, v)
	}
}
