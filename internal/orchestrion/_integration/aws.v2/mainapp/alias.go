// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under another name than aws, which the rules must handle the way
// orchestrion does.
import (
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
)

func aliasedValue() awssdk.Config {
	return awssdk.Config{Region: region, Credentials: creds}
}

func aliasedNew() awssdk.Config {
	cfg := awssdk.NewConfig()
	cfg.Region = region
	cfg.Credentials = creds
	return *cfg
}
