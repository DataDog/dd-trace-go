// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageDatabaseSQL)
}

// ParseDSN parses various supported DSN types into a map of key/value pairs which can be used as valid tags.
func ParseDSN(driverName, dsn string) (meta map[string]string, err error) {
	meta = make(map[string]string)
	switch driverName {
	case "mysql":
		meta, err = parseMySQLDSN(dsn)
		if err != nil {
			instr.Logger().Debug("Error parsing DSN for mysql: %v", sanitizeError(err))
			return
		}
	case "postgres", "pgx":
		meta, err = parsePostgresDSN(dsn)
		if err != nil {
			instr.Logger().Debug("Error parsing DSN for postgres: %v", sanitizeError(err))
			return
		}
	case "sqlserver":
		meta, err = parseSQLServerDSN(dsn)
		if err != nil {
			instr.Logger().Debug("Error parsing DSN for sqlserver: %v", sanitizeError(err))
			return
		}
	default:
		// Try to parse the DSN and see if the scheme contains a known driver name.
		u, e := parseSafe(dsn)
		if e != nil {
			// dsn is not a valid URL, so just ignore
			instr.Logger().Debug("Error parsing driver name from DSN: %v", e)
			return
		}
		if driverName != u.Scheme {
			// In some cases the driver is registered under a non-official name.
			// For example, "Test" may be the registered name with a DSN of "postgres://postgres:postgres@127.0.0.1:5432/fakepreparedb"
			// for the purposes of testing/mocking.
			// In these cases, we try to parse the DSN based upon the DSN itself, instead of the registered driver name
			return ParseDSN(u.Scheme, dsn)
		}
	}
	return reduceKeys(meta), nil
}

// reduceKeys takes a map containing parsed DSN information and returns a new
// map containing only the keys relevant as tracing tags, if any.
func reduceKeys(meta map[string]string) map[string]string {
	var keysOfInterest = map[string]string{
		"user":                             ext.DBUser,
		"application_name":                 ext.DBApplication,
		"dbname":                           ext.DBName,
		"host":                             ext.TargetHost,
		"port":                             ext.TargetPort,
		ext.MicrosoftSQLServerInstanceName: ext.MicrosoftSQLServerInstanceName,
	}
	m := make(map[string]string)
	for k, v := range meta {
		if nk, ok := keysOfInterest[k]; ok {
			m[nk] = v
		}
	}
	return m
}

// parseMySQLDSN parses a mysql-type dsn into a map.
func parseMySQLDSN(dsn string) (map[string]string, error) {
	cfg, err := mySQLConfigFromDSN(dsn)
	if err != nil {
		return nil, err
	}
	host, port, _ := net.SplitHostPort(cfg.Addr)
	meta := map[string]string{
		"user":   cfg.User,
		"host":   host,
		"port":   port,
		"dbname": cfg.DBName,
	}
	return meta, nil
}

// parsePostgresDSN parses a postgres-type dsn into a map.
func parsePostgresDSN(dsn string) (map[string]string, error) {
	var err error
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		// url form, convert to opts
		dsn, err = parseURL(dsn)
		if err != nil {
			return nil, err
		}
	}
	meta := make(map[string]string)
	if err := parseOpts(dsn, meta); err != nil {
		return nil, err
	}
	// remove sensitive information
	delete(meta, "password")
	return meta, nil
}

// parseSQLServerDSN parses a sqlserver-type dsn into a map
func parseSQLServerDSN(dsn string) (map[string]string, error) {
	var err error
	var meta map[string]string
	if strings.HasPrefix(dsn, "sqlserver://") {
		// url form
		meta, err = parseSQLServerURL(dsn)
		if err != nil {
			return nil, err
		}
	} else {
		meta, err = parseSQLServerADO(dsn)
		if err != nil {
			return nil, err
		}
	}
	delete(meta, "password")
	return meta, nil
}

// parseSafe behaves like url.Parse, but if parsing fails it returns an
// error with any credential part of the URL already scrubbed.
func parseSafe(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err == nil {
		return u, nil
	}
	// url.Parse always wraps the real problem in *url.Error.
	var ue *url.Error
	if !errors.As(err, &ue) {
		return nil, err
	}
	return nil, sanitizeURLError(ue)
}

// sanitizeURLError returns a copy of e whose URL field is redacted.
func sanitizeURLError(e *url.Error) *url.Error {
	// Best-case: we can still parse enough to use URL.Redacted().
	if parsed, perr := url.Parse(e.URL); perr == nil {
		e.URL = parsed.Redacted()
		return e
	}

	// Fallback: use the comprehensive sanitize() function for all password patterns.
	e.URL = sanitize(e.URL)
	return e
}

// sanitizeError returns an error with sensitive information redacted.
// It handles both URL errors and general errors containing DSN information.
func sanitizeError(err error) error {
	if err == nil {
		return nil
	}

	// Check if it's a URL error and use the existing sanitizer.
	var ue *url.Error
	if errors.As(err, &ue) {
		return sanitizeURLError(ue)
	}

	// For other errors, sanitize the error message.
	return fmt.Errorf("%s", sanitize(err.Error()))
}

func sanitize(msg string) string {
	msg = sanitizeKeyValuePasswords(msg)
	msg = sanitizeURLPasswords(msg)
	msg = sanitizeMySQLPasswords(msg)
	return msg
}

// Compiled regex patterns for password sanitization - compiled once at package init
var (
	keyValueSpacePattern = regexp.MustCompile(`(?i)(password|passwd|pwd)\s*=\s*(.+?)(\s+(?:host|user|port|database|dbname)\s*=)`)
	keyValueSemiPattern  = regexp.MustCompile(`(?i)(password|passwd|pwd)\s*=\s*([^;]+)(;)`)
	keyValuePattern      = regexp.MustCompile(`(?i)(password|passwd|pwd)\s*=\s*([^\s;]+)`)
)

// sanitizeKeyValuePasswords sanitizes password values in key=value format
func sanitizeKeyValuePasswords(msg string) string {
	result := keyValueSpacePattern.ReplaceAllString(msg, `$1=xxxxx$3`)
	result = keyValueSemiPattern.ReplaceAllString(result, `$1=xxxxx$3`)
	result = keyValuePattern.ReplaceAllString(result, `$1=xxxxx`)
	return result
}

// sanitizeURLPasswords sanitizes passwords in URL format (user:pass@host).
func sanitizeURLPasswords(msg string) string {
	// Look for URL patterns and manually handle them to avoid issues with @ in passwords.
	urlStart := strings.Index(msg, "://")
	if urlStart == -1 {
		return msg
	}

	// Find the start of the credentials section
	credStart := urlStart + 3

	// Look for the rightmost @ that separates credentials from host
	hostStart := -1
	for i := len(msg) - 1; i >= credStart; i-- {
		if msg[i] == '@' {
			// Check if this @ is followed by what looks like a hostname
			remaining := msg[i+1:]
			if hostIdx := strings.IndexAny(remaining, " /\"?"); hostIdx != -1 {
				remaining = remaining[:hostIdx]
			}
			// Simple hostname validation: starts with alphanumeric, contains valid hostname chars
			if len(remaining) > 0 && isValidHostnameStart(remaining) {
				hostStart = i
				break
			}
		}
	}

	if hostStart == -1 {
		return msg
	}

	// Find the colon that separates username from password
	credSection := msg[credStart:hostStart]
	colonIdx := strings.Index(credSection, ":")
	if colonIdx == -1 {
		return msg
	}

	// Replace the password
	absoluteColonIdx := credStart + colonIdx
	result := msg[:absoluteColonIdx+1] + "xxxxx" + msg[hostStart:]
	return result
}

// isValidHostnameStart checks if a string starts like a valid hostname
func isValidHostnameStart(s string) bool {
	if len(s) == 0 {
		return false
	}
	// Must start with alphanumeric
	first := s[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || (first >= '0' && first <= '9')) {
		return false
	}
	// Should contain hostname-like patterns
	return strings.Contains(s, ".") || strings.Contains(s, ":") ||
		   strings.Contains(s, "/") || s == strings.TrimSpace(s)
}

// sanitizeMySQLPasswords sanitizes passwords in MySQL DSN format (user:pass@tcp...).
func sanitizeMySQLPasswords(msg string) string {
	// Find @tcp( and work backwards to find the username:password pattern.
	tcpIndex := strings.Index(msg, "@tcp(")
	if tcpIndex == -1 {
		return msg
	}

	// Work backwards from @tcp( to find the start of username:password.
	start := tcpIndex - 1
	for start >= 0 && msg[start] != ' ' && msg[start] != '\t' && msg[start] != ':' {
		start--
	}

	// If we stopped at a colon, we need to find the username before it.
	if start >= 0 && msg[start] == ':' {
		// Continue backwards to find the start of the username.
		userStart := start - 1
		for userStart >= 0 && (msg[userStart] >= 'a' && msg[userStart] <= 'z' ||
			msg[userStart] >= 'A' && msg[userStart] <= 'Z' ||
			msg[userStart] >= '0' && msg[userStart] <= '9' ||
			msg[userStart] == '_') {
			userStart--
		}
		userStart++ // Move to the first character of the username.

		// Replace the password part.
		username := msg[userStart:start]
		result := msg[:userStart] + username + ":xxxxx@tcp(" + msg[tcpIndex+5:]
		return result
	}

	return msg
}
