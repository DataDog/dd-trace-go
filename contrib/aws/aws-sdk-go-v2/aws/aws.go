// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws"
)

// AppendMiddleware takes the aws.Config and adds the Datadog tracing middleware into the APIOptions middleware stack.
// See https://aws.github.io/aws-sdk-go-v2/docs/middleware for more information.
func AppendMiddleware(awsCfg *aws.Config, opts ...Option) {
	v2.AppendMiddleware(awsCfg, opts...)
}
