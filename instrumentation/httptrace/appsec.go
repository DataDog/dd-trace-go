// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptrace

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	appsectrace "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
)

type otelAppSecSpanTagSetter struct {
	appsectrace.TagSetter
}

func (s otelAppSecSpanTagSetter) SetTag(key string, value any) {
	switch key {
	case ext.HTTPClientIP:
		key = ext.ClientAddress
	case ext.NetworkClientIP:
		key = ext.NetworkPeerAddress
	}
	s.TagSetter.SetTag(key, value)
}

// AppSecSpanTagSetter applies OpenTelemetry HTTP semantic names to tags that
// AppSec writes to spans. This affects trace metadata only; values sent to the
// WAF, including http.client_ip, are unchanged.
func AppSecSpanTagSetter(setter appsectrace.TagSetter, otelSemanticsEnabled bool) appsectrace.TagSetter {
	if otelSemanticsEnabled {
		return otelAppSecSpanTagSetter{TagSetter: setter}
	}
	return setter
}
