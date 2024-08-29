// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package wafsec

import (
	"sync/atomic"
	"time"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v3"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

type (
	WAFContextOperation struct {
		dyngo.Operation
		trace.SecurityEventsHolder

		Ctx     atomic.Pointer[waf.Context]
		Handle  *waf.Handle
		Limiter limiter.Limiter
		Timeout time.Duration
	}

	HTTPArgs struct {
	}

	HTTPRes struct{}

	GRPCArgs struct {
	}

	GRPCRes struct{}

	GraphQLArgs struct {
	}

	GraphQLRes struct{}
)

func (HTTPArgs) IsArgOf(*WAFContextOperation)   {}
func (HTTPRes) IsResultOf(*WAFContextOperation) {}

func (GRPCArgs) IsArgOf(*WAFContextOperation)   {}
func (GRPCRes) IsResultOf(*WAFContextOperation) {}

func (GraphQLArgs) IsArgOf(*WAFContextOperation)   {}
func (GraphQLRes) IsResultOf(*WAFContextOperation) {}
