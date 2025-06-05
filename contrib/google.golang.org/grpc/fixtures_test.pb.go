// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package grpc

import (
	v2 "github.com/DataDog/dd-trace-go/instrumentation/testutils/grpc/v2/fixturepb"
)

// The request message containing the user's name.
type FixtureRequest = v2.FixtureRequest

// The response message containing the greetings
type FixtureReply = v2.FixtureReply
