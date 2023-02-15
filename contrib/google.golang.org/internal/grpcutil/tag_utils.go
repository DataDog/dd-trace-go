// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcutil // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/internal/grpcutil"

import (
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

// ExtractRPCTags will assign the proper tag values for method, service, package according to otel given a full method
func ExtractRPCTags(fullMethod string) map[string]string {

	// Otel definition: https://opentelemetry.io/docs/reference/specification/trace/semantic_conventions/rpc/#span-name

	tags := map[string]string{
		ext.RPCMethod:   "",
		ext.RPCService:  "",
		ext.GRPCPackage: "",
	}

	//Always remove leading slash
	fullMethod = strings.TrimPrefix(fullMethod, "/")

	//Split by slash and get everything after last slash as method
	slashSplit := strings.SplitAfter(fullMethod, "/")
	tags[ext.RPCMethod] = slashSplit[len(slashSplit)-1]

	//Join everything before last slash and remove last slash as service
	tags[ext.RPCService] = strings.TrimSuffix(strings.Join(slashSplit[:len(slashSplit)-1], ""), "/")

	//Split by period and see if package exists if period is found
	if strings.Contains(tags[ext.RPCService], ".") {
		dotSplit := strings.SplitAfter(tags[ext.RPCService], ".")
		tags[ext.GRPCPackage] = strings.TrimSuffix(strings.Join(dotSplit[:len(dotSplit)-1], ""), ".")
	}

	return tags
}
