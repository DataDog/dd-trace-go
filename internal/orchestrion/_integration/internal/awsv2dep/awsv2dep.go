// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package awsv2dep builds aws.Config values inside a dependency module.
package awsv2dep

import (
	"github.com/aws/aws-sdk-go-v2/aws"
)

// Configs returns one config per construction style, keyed by style: a value
// literal, a pointer literal and aws.NewConfig.
func Configs(region string, creds aws.CredentialsProvider) map[string]aws.Config {
	value := aws.Config{Region: region, Credentials: creds}

	ptr := &aws.Config{Region: region, Credentials: creds}

	newCfg := aws.NewConfig()
	newCfg.Region = region
	newCfg.Credentials = creds

	return map[string]aws.Config{
		"value":   value,
		"pointer": *ptr,
		"new":     *newCfg,
	}
}
