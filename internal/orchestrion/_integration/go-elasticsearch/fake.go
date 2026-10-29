// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package go_elasticsearch

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/esfake"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// startFake starts a fake Elasticsearch node for the test, serving TLS when
// tlsConfig is not nil, and returns its URL.
func startFake(t *testing.T, tlsConfig *tls.Config) string {
	t.Helper()
	srv, err := esfake.Start(tlsConfig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	return srv.URL
}

// perform sends GET /<key> with client, so each client's span has its own
// resource.
func perform(ctx context.Context, t *testing.T, client esClient, key string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/"+key, nil)
	require.NoError(t, err)
	res, err := client.Perform(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

// querySpan is the span of a GET /<key> request sent by a traced client.
func querySpan(key string) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "elasticsearch.query",
			"service":  "elastic.client",
			"resource": "GET /" + key,
			"type":     "elasticsearch",
		},
		Meta: map[string]string{
			"component":            "elastic/go-elasticsearch.v6",
			"span.kind":            "client",
			"db.system":            "elasticsearch",
			"elasticsearch.method": "GET",
			"elasticsearch.url":    "/" + key,
		},
	}
}

// httpSpan is the net/http span of the round trip of a GET /<key> request.
// The contrib starts its span without passing it on in the request context,
// so the net/http span is a sibling of the elasticsearch.query span.
func httpSpan(key string) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "http.request",
			"type":     "http",
			"resource": "GET /" + key,
		},
		Meta: map[string]string{
			"component": "net/http",
			"span.kind": "client",
		},
	}
}

// countingTransport counts the requests it sends with http.DefaultTransport.
type countingTransport struct {
	calls atomic.Int32
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return http.DefaultTransport.RoundTrip(req)
}

// selfSigned returns a TLS config for a node on 127.0.0.1, its certificate as
// PEM, and the certificate's SHA-256 fingerprint in hex, the format of
// Config.CertificateFingerprint.
func selfSigned(t *testing.T) (*tls.Config, []byte, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "esfake"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	fingerprint := sha256.Sum256(der)
	cfg := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	}
	return cfg, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), hex.EncodeToString(fingerprint[:])
}
