// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v2"
	"github.com/datadog/dd-trace-go/dyngo/domain"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/graphqlsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/grpcsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/httpsec"
)

var dyngoProductConfigurations = []func(
	*waf.Handle,
	sharedsec.Actions,
	*config.Config,
	limiter.Limiter,
){
	graphqlsec.Product.Configure,
	grpcsec.Product.Configure,
	httpsec.Product.Configure,
}

func init() {
	// ASM's priority must be greater than that of APM, so that spans get created
	// before ASM attempts using them as transport, and don't get finished before
	// it's finished needing them.
	const asmPriority = 100

	domain.GraphQL.RegisterProduct(graphqlsec.Product, asmPriority)
	domain.GRPC.RegisterProduct(grpcsec.Product, asmPriority)
	domain.HTTP.RegisterProduct(httpsec.Product, asmPriority)
}
