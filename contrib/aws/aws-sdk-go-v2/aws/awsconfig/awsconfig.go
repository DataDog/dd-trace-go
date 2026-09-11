// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package awsconfig provides a github.com/aws/aws-sdk-go-v2/config.LoadOptionsFunc
// that adds Datadog tracing to an AWS config, for use when the aws.Config isn't
// otherwise directly accessible (e.g. it is built by github.com/aws/aws-sdk-go-v2/config.LoadDefaultConfig).
//
// This package is separate from the parent aws contrib package so Orchestrion can
// keep instrumenting github.com/aws/aws-sdk-go-v2/aws.Config construction (including
// inside github.com/aws/aws-sdk-go-v2/config itself) without an import cycle: the
// parent aws package is woven into every aws.Config{} literal, including the ones
// aws-sdk-go-v2/config builds internally, so the parent package must not import
// aws-sdk-go-v2/config itself.
package awsconfig

import (
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfigsdk "github.com/aws/aws-sdk-go-v2/config"

	awstrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/aws"
)

// WithDataDogTracer returns an AWS config LoadOptionsFunc that adds the Datadog tracing middleware into the
// APIOptions middleware stack. It can be passed to (github.com/aws/aws-sdk-go-v2/config).LoadDefaultConfig when
// the aws.Config isn't otherwise directly accessible.
// See https://aws.github.io/aws-sdk-go-v2/docs/middleware for more information.
func WithDataDogTracer(opts ...awstrace.Option) awsconfigsdk.LoadOptionsFunc {
	return func(o *awsconfigsdk.LoadOptions) error {
		var cfg awssdk.Config
		cfg.APIOptions = o.APIOptions
		awstrace.AppendMiddleware(&cfg, opts...)
		o.APIOptions = cfg.APIOptions
		return nil
	}
}
