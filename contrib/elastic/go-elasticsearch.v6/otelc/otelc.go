// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules for elastic/go-elasticsearch.v6 and the
// functions they call. See otelc.yaml.
package otelc

import (
	"net/http"

	elastictrace "github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2"
)

// Transport returns the Transport for a client config that sets rt, caCert
// and fingerprint as its Transport, CACert and CertificateFingerprint, so that
// the client is traced.
//
// go-elasticsearch applies a CA certificate or a certificate fingerprint only
// to an *http.Transport, so a config that sets either keeps rt. A transport
// that already traces requests is kept too.
func Transport(rt http.RoundTripper, caCert []byte, fingerprint string) http.RoundTripper {
	if caCert != nil || fingerprint != "" || elastictrace.IsTraced(rt) {
		return rt
	}
	if rt == nil {
		return elastictrace.NewRoundTripper()
	}
	return elastictrace.NewRoundTripper(elastictrace.WithTransport(rt))
}
