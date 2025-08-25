// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httpsec

import (
	"net"
	"net/netip"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
)

// ClientIP returns the first public IP address found in the given headers. If
// none is present, it returns the first valid IP address present, possibly
// being a local IP address. The remote address, when valid, is used as fallback
// when no IP address has been found at all.
func ClientIP(hdrs map[string][]string, hasCanonicalHeaders bool, remoteAddr string, monitoredHeaders []string) (remoteIP, clientIP netip.Addr) {
	// Walk IP-related headers
	var foundIP netip.Addr
headersLoop:
	for _, headerName := range monitoredHeaders {
		if hasCanonicalHeaders {
			headerName = textproto.CanonicalMIMEHeaderKey(headerName)
		}

		headerValues, exists := hdrs[headerName]
		if !exists {
			continue // this monitored header is not present
		}

		// Assuming a list of comma-separated IP addresses, split them and build
		// the list of values to try to parse as IP addresses
		var ips []string
		for _, headerValue := range headerValues {
			if strings.ToLower(headerName) == "forwarded" {
				ips = append(ips, parseForwardedHeader(headerValue)...)
			} else {
				ips = append(ips, strings.Split(headerValue, ",")...)
			}
		}

		// Look for the first valid or global IP address in the comma-separated list
		for _, ipstr := range ips {
			ip := parseIP(strings.TrimSpace(ipstr))
			if !ip.IsValid() {
				continue
			}
			// Replace foundIP if still not valid in order to keep the oldest
			if !foundIP.IsValid() {
				foundIP = ip
			}
			if isGlobalIP(ip) {
				foundIP = ip
				break headersLoop
			}
		}
	}

	// Decide which IP address is the client one by starting with the remote IP
	if ip := parseIP(remoteAddr); ip.IsValid() {
		remoteIP = ip
		clientIP = ip
	}

	// The IP address found in the headers supersedes a private remote IP address.
	if foundIP.IsValid() && !isGlobalIP(remoteIP) || isGlobalIP(foundIP) {
		clientIP = foundIP
	}

	return remoteIP, clientIP
}

var (
	forwardedPortV64Re = regexp.MustCompile(`^(\[[^]]+\]|\d+\.\d+\.\d+\.\d+):\d+$`)
	forwardedPortV4Re  = regexp.MustCompile(`^(\d+\.\d+\.\d+\.\d+):\d+$`)
)

// parseForwardedHeader parses the value of the `Forwarded` header, returning
// the values of all `for` directives it contains, in the order they appear.
// Values may not always be IP addresses; but those values that are will have
// any quoting and port information removed.
//
// If the value is found to be syntactically incorrect, a null slice is returned.
//
// See: https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Forwarded
func parseForwardedHeader(rest string) []string {
	var result []string

	// The Forwarded header is a semicolon-separated list of directives such as:
	// Forwarded: for=192.0.2.60;proto=http;by=203.0.113.43
	// The values MAY be quoted (using double quoted `'"'`), but IPv6 addresses
	// MUST be quoted and enclosed in square brackers. IP addresses may include
	// port information as well, but are not required to.

	for rest != "" {
		directive, tail, ok := strings.Cut(rest, "=")
		if !ok {
			// Expected a directive, this appears to be invalid...
			log.Debug("invalid Forwarded header value: expected directive, but no '=' was found")
			return nil
		}
		tail = strings.TrimLeftFunc(tail, unicode.IsSpace)

		var (
			value  string
			quoted bool
		)
		if len(tail) != 0 && tail[0] == '"' {
			var closed bool
			for i := 1; i < len(tail); i++ {
				if tail[i] == '"' {
					value = tail[:i+1]
					rest = tail[i+1:]
					quoted = true
					closed = true
					break
				}
				if tail[i] == '\\' {
					i++ // The next character is escaped
				}
			}
			if !closed {
				// Unclosed quoted value, this is invalid!
				log.Debug("invalid Forwarded header value: a quoted value was not closed")
				return nil
			}
		} else {
			var foundSemi bool
			for i, c := range tail {
				if c == ';' {
					value = tail[:i]
					rest = tail[i:]
					foundSemi = true
					break
				}
			}
			if !foundSemi {
				value = tail
				rest = ""
			}
		}

		if strings.ToLower(directive) == "for" {
			// This is the directive we're interested in...
			if quoted {
				// There may be an IPv6 address enclosed in square brackets here.
				value, err := strconv.Unquote(value)
				if err != nil {
					log.Debug("invalid Forwarded header value: invalid quoted value: %v", err)
					return nil
				}
				// Remove any port information from the value.
				if m := forwardedPortV64Re.FindStringSubmatch(value); m != nil {
					value = m[1]
				}
				// Remove IPv6 brackets if present.
				if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
					value = value[1 : len(value)-1]
				}
				// We have our IP address (or identifier) here.
				result = append(result, value)
			} else {
				// Remove any port information from the value.
				if m := forwardedPortV4Re.FindStringSubmatch(value); m != nil {
					value = m[1]
				}
				// We have our IP address (or identifier) here.
				result = append(result, value)
			}
		}

		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		if rest != "" {
			if rest[0] != ';' {
				// Expected a semicolon, this appears to be invalid...
				log.Debug("invalid Forwarded header value: a semicolon was expected between directives")
				return nil
			}
			rest = rest[1:]
			rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		}
	}
	return result
}

func parseIP(s string) netip.Addr {
	if ip, err := netip.ParseAddr(s); err == nil {
		return ip
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		if ip, err := netip.ParseAddr(h); err == nil {
			return ip
		}
	}
	return netip.Addr{}
}

var (
	ipv6SpecialNetworks = [...]netip.Prefix{
		netip.MustParsePrefix("fec0::/10"), // site local
	}

	// This IP block is not routable on internet and an industry standard/trend
	// is emerging to use it for traditional IT-managed networking environments
	// with limited RFC1918 space allocations. This is also frequently used by
	// kubernetes pods' internal networking. It is hence deemed private for the
	// purpose of Client IP extraction.
	k8sInternalIPv4Prefix = netip.MustParsePrefix("100.65.0.0/10")
)

func isGlobalIP(ip netip.Addr) bool {
	// IsPrivate also checks for ipv6 ULA.
	// We care to check for these addresses are not considered public, hence not global.
	// See https://www.rfc-editor.org/rfc/rfc4193.txt for more details.
	isGlobal := ip.IsValid() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !k8sInternalIPv4Prefix.Contains(ip)
	if !isGlobal || !ip.Is6() {
		return isGlobal
	}
	for _, n := range ipv6SpecialNetworks {
		if n.Contains(ip) {
			return false
		}
	}
	return isGlobal
}
