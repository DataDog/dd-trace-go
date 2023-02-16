// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcutil // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/internal/grpcutil"

import (
	"strings"
)

// RPCTags holds the values for various RPC tags to be set in other instances
type RPCTags struct {
	Method  string
	Service string
	Package string
}

// ExtractRPCTags will assign the proper tag values for method, service, package according to otel given a full method
func ExtractRPCTags(fullMethod string) RPCTags {

	// Otel definition: https://opentelemetry.io/docs/reference/specification/trace/semantic_conventions/rpc/#span-name
	// Expected fullmethod format => $package.$service/$method

	tags := RPCTags{
		Method:  "",
		Service: "",
		Package: "",
	}

	elems := strings.Split(strings.TrimPrefix(fullMethod, "/"), "/")
	if len(elems) < 2 { //Empty string check
		tags.Method = ""
		tags.Service = ""
		tags.Package = ""
		return tags
	} else if len(elems) > 2 { //Improper grpc fullmethod check
		tags.Method = "unknown"
		tags.Service = "unknown"
		tags.Package = "unknown"
		return tags
	}

	tags.Service = elems[0]
	tags.Method = elems[1]

	dotSplit := strings.SplitAfter(tags.Service, ".")
	if len(dotSplit) >= 2 { //Package existence check
		tags.Package = strings.TrimSuffix(strings.Join(dotSplit[:len(dotSplit)-1], ""), ".")
	}

	return tags
}
