// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptrace

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

type authorityPortState uint8

const (
	authorityPortAbsent authorityPortState = iota
	authorityPortValid
	authorityPortInvalid
)

type parsedHTTPAuthority struct {
	address   string
	port      int
	portState authorityPortState
	valid     bool
}

// ServerAddressPortFromClientRequest returns OpenTelemetry `server.address` and `server.port` from
// `req`'s effective request authority (https://www.rfc-editor.org/rfc/rfc9110.html#name-host-and-authority).
//
// The effective authority is: URL host/port for absolute requests, otherwise the authority header (https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/http/http-spans.md?plain=1#L203-L215).
// This function seems to implement the opposite by preferring the authority field.
// That's because [http.DefaultTransport] overrides the URL's host/port with the
// non-empty authority field, even for absolute-form request targets.
// So the effective authority is always the authority field when present.
//
// The authority is not the network address, which is covered by `network.peer.*`.
//
// Port is inferred from HTTP schemes ("80" or "443") when absent from the effective
// authority (https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/non-normative/http-migration.md?plain=1#L79);
// explicit but invalid ports or non-HTTP schemes return port -1.
func ServerAddressPortFromClientRequest(req *http.Request) (address string, port int) {
	authority := req.Host
	if authority == "" {
		authority = req.URL.Host
	}
	parsed := parseHTTPAuthority(authority)
	if !parsed.valid {
		return authority, -1
	}
	if parsed.portState == authorityPortAbsent {
		return parsed.address, defaultPort(req.URL.Scheme)
	}
	return parsed.address, parsed.port
}

func parseHTTPAuthority(authority string) parsedHTTPAuthority {
	// We need to do two things with the `authority` string:
	// 1. Split it into host and port.
	// 2. Clean up possible IPv6 hosts by removing brackets.
	//
	// `net.SplitHostPort` does almost that, but errs on authorities without port.
	//
	// We chose to check for the presence of a port before calling `SplitHostPort`,
	// instead of calling it and handling the error, since the complexity is similar:
	// both approaches require IPv6 bracket detection.

	// Look for `:`, but don't conflate it with IPv6 delimiters.
	if strings.HasPrefix(authority, "[") {
		addressEnd := strings.LastIndexByte(authority, ']')
		if addressEnd < 0 {
			return parsedHTTPAuthority{}
		}
		if addressEnd == len(authority)-1 {
			return parsedHTTPAuthority{
				address:   authority[1:addressEnd],
				port:      -1,
				portState: authorityPortAbsent,
				valid:     true,
			}
		}
		if authority[addressEnd+1] != ':' {
			return parsedHTTPAuthority{}
		}
	} else if !strings.Contains(authority, ":") {
		return parsedHTTPAuthority{
			address:   authority,
			port:      -1,
			portState: authorityPortAbsent,
			valid:     true,
		}
	}

	address, portString, err := net.SplitHostPort(authority)
	if err != nil {
		return parsedHTTPAuthority{}
	}
	// Check if the port is an integer and, as a bonus, within the valid range (0-65535).
	parsedPort, err := strconv.ParseUint(portString, 10, 16)
	if err != nil {
		return parsedHTTPAuthority{
			address:   address,
			port:      -1,
			portState: authorityPortInvalid,
			valid:     true,
		}
	}
	return parsedHTTPAuthority{
		address:   address,
		port:      int(parsedPort),
		portState: authorityPortValid,
		valid:     true,
	}
}

// The inferred default port from HTTP schemes.
func defaultPort(scheme string) int {
	switch {
	case strings.EqualFold(scheme, "http"):
		return 80
	case strings.EqualFold(scheme, "https"):
		return 443
	default:
		return -1
	}
}
