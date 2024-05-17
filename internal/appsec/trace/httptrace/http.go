// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"net/netip"

	"github.com/DataDog/appsec-internal-go/httpsec"
)

var (
	// monitoredClientIPHeadersCfg is the list of IP-related headers leveraged to
	// retrieve the public client IP address in ClientIP. This is defined at init
	// time in function of the value of the envClientIPHeader environment variable.
	monitoredClientIPHeadersCfg []string
)

// ClientIPTags returns the resulting Datadog span tags `http.client_ip`
// containing the client IP and `network.client.ip` containing the remote IP.
// The tags are present only if a valid ip address has been returned by
// ClientIP().
func ClientIPTags(headers map[string][]string, hasCanonicalHeaders bool, remoteAddr string) (tags map[string]string, clientIP netip.Addr) {
	remoteIP, clientIP := httpsec.ClientIP(headers, hasCanonicalHeaders, remoteAddr, monitoredClientIPHeadersCfg)
	tags = httpsec.ClientIPTags(remoteIP, clientIP)
	return tags, clientIP
}
