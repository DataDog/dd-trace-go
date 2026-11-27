// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
)

func init() {
	registerLibrary("aws_v1", func() {
		_ = session.Must(session.NewSession(&aws.Config{
			Credentials: credentials.AnonymousCredentials,
			Region:      new("us-east-1"),
		}))
	})
}
