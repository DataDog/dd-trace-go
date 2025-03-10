// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//nolint:revive
package civisibility

// Let's import all the internal package so we can enable the go:linkname directive over the internal packages
// This will be useful for dogfooding in dd-go by using a shim package that will call the internal package
import (
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting/coverage"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/net"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/telemetry"
)
