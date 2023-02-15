// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcutil // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/internal/grpcutil"

import (
	"strings"
)

type RPCTags struct {
	Method  string
	Service string
	Package string
}

// ExtractRPCTags will assign the proper tag values for method, service, package according to otel given a full method
func ExtractRPCTags(fullMethod string) RPCTags {

	// Otel definition: https://opentelemetry.io/docs/reference/specification/trace/semantic_conventions/rpc/#span-name

	tags := RPCTags{
		Method:  "",
		Service: "",
		Package: "",
	}

	//Split by slash and get everything after last slash as method
	slashSplit := strings.SplitAfter(fullMethod, "/")
	tags.Method = slashSplit[len(slashSplit)-1]

	//Join everything before last slash and remove last slash as service
	tags.Service = strings.TrimSuffix(strings.Join(slashSplit[:len(slashSplit)-1], ""), "/")

	//Split by period and see if package exists assuming period is found
	if strings.Contains(tags.Service, ".") {
		dotSplit := strings.SplitAfter(tags.Service, ".")
		tags.Package = strings.TrimSuffix(strings.Join(dotSplit[:len(dotSplit)-1], ""), ".")
	}

	return tags
}
