// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

// Logger is the logger used in this package, intended to be assigned from the
// instrumentation package logger loaded at init time in `aws`.
// This is created to avoid registering more `aws-sdk-go-v2` contribs, as Serverless
// requires only
var Logger instrumentation.Logger
