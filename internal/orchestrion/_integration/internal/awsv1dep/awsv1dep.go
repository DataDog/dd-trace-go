// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package awsv1dep creates aws-sdk-go sessions inside a dependency module.
package awsv1dep

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
)

// NewSession returns a session built from cfg.
func NewSession(cfg *aws.Config) (*session.Session, error) {
	return session.NewSession(cfg)
}
