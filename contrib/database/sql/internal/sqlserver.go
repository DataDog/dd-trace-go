// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"errors"
	"fmt"
	"net"
	nurl "net/url"
	"strings"
	"unicode"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

func parseSQLServerURL(url string) (map[string]string, error) {
	u, err := nurl.Parse(url)
	if err != nil {
		// The error from net/url quotes the whole string, password included.
		return nil, errors.New("invalid sqlserver connection URL")
	}

	if u.Scheme != "sqlserver" {
		return nil, fmt.Errorf("invalid connection protocol: %s", u.Scheme)
	}

	kvs := map[string]string{}
	escaper := strings.NewReplacer(` `, `\ `, `'`, `\'`, `\`, `\\`)
	accrue := func(k, v string) {
		if v != "" {
			kvs[k] = escaper.Replace(v)
		}
	}

	if u.User != nil {
		v := u.User.Username()
		accrue("user", v)
	}

	if host, port, err := net.SplitHostPort(u.Host); err != nil {
		accrue("host", u.Host)
	} else {
		accrue("host", host)
		accrue("port", port)
	}

	if u.Path != "" {
		accrue(ext.MicrosoftSQLServerInstanceName, u.Path[1:])
	}

	q := u.Query()
	for k := range q {
		// go-mssqldb lowercases the parameter names of the URL form.
		if strings.EqualFold(k, "database") {
			accrue("dbname", q.Get(k))
		}
	}

	return kvs, nil
}

var keySynonyms = map[string]string{
	"server":          "host",
	"data source":     "host",
	"address":         "host",
	"network address": "host",
	"addr":            "host",
	"uid":             "user",
	"user id":         "user",
	"initial catalog": "dbname",
	"database":        "dbname",
}

// setSQLServerParam stores a pair read from an ADO connection string under the
// name used for tags.
func setSQLServerParam(kvs map[string]string, key, val string) {
	if synonym, found := keySynonyms[key]; found {
		key = synonym
	}
	if key == "host" {
		val = strings.TrimPrefix(val, "tcp:")
		hostParts := strings.Split(val, ",")
		if len(hostParts) == 2 && len(hostParts[1]) > 0 {
			val = hostParts[0]
			kvs["port"] = hostParts[1]
		}
	}
	kvs[key] = val
}

// odbcKeys maps the keys go-mssqldb reads from an "odbc:" string to the names
// used for tags. The driver knows no synonym for them, and it takes the value
// of server and port as they are.
var odbcKeys = map[string]string{
	"server":   "host",
	"port":     "port",
	"user id":  "user",
	"database": "dbname",
}

// setSQLServerODBCParam stores a pair read from an ODBC connection string. Keys
// that the driver does not read for the connection are dropped, so that one
// named like a tag, such as host, cannot end up in the tags.
func setSQLServerODBCParam(kvs map[string]string, key, val string) {
	if name, ok := odbcKeys[key]; ok {
		kvs[name] = val
	}
}

// parseSQLServerADO reads the "key=value;key=value" form.
//
// go-mssqldb used to cut this form at every semicolon. Since v1.9.3 a value
// can be wrapped in double quotes to hold a semicolon, with "" for a literal
// quote. The driver version is not known here and the two readings can differ,
// so when the string has a quote only the pairs they agree on are kept.
// Otherwise a piece of a password could be taken for another parameter.
func parseSQLServerADO(dsn string) (map[string]string, error) {
	kvs := readSQLServerADO(strings.Split(dsn, ";"), false)
	if !strings.Contains(dsn, `"`) {
		return kvs, nil
	}
	quoted := readSQLServerADO(splitSQLServerADOQuoted(dsn), true)
	for k, v := range kvs {
		if qv, ok := quoted[k]; !ok || qv != v {
			delete(kvs, k)
		}
	}
	return kvs, nil
}

// readSQLServerADO turns the fields of an ADO connection string into pairs.
// With unquote set, a value wrapped in double quotes loses them.
func readSQLServerADO(fields []string, unquote bool) map[string]string {
	kvs := map[string]string{}
	for _, f := range fields {
		if len(f) == 0 {
			continue
		}
		pts := strings.SplitN(f, "=", 2)
		key := strings.TrimSpace(strings.ToLower(pts[0]))
		if len(key) == 0 {
			continue
		}
		val := ""
		if len(pts) > 1 {
			val = strings.TrimSpace(pts[1])
			if unquote && len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
				val = strings.ReplaceAll(val[1:len(val)-1], `""`, `"`)
			}
		}
		setSQLServerParam(kvs, key, val)
	}
	return kvs
}

// splitSQLServerADOQuoted cuts an ADO connection string at the semicolons
// that are outside double quotes.
func splitSQLServerADOQuoted(dsn string) []string {
	var fields []string
	inQuotes := false
	start := 0
	for i := 0; i < len(dsn); i++ {
		switch dsn[i] {
		case '"':
			if inQuotes && i+1 < len(dsn) && dsn[i+1] == '"' {
				i++ // "" inside quotes is a literal quote
				continue
			}
			inQuotes = !inQuotes
		case ';':
			if !inQuotes {
				fields = append(fields, dsn[start:i])
				start = i + 1
			}
		}
	}
	return append(fields, dsn[start:])
}

// parseSQLServerODBC reads the pairs behind an "odbc:" prefix the way
// go-mssqldb does. A value can be wrapped in braces to hold a semicolon, with
// }} for a literal closing brace.
func parseSQLServerODBC(dsn string) (map[string]string, error) {
	kvs := map[string]string{}
	separator := func(r rune) bool { return r == ';' || unicode.IsSpace(r) }
	for {
		dsn = strings.TrimLeftFunc(dsn, separator)
		if dsn == "" {
			return kvs, nil
		}
		i := strings.IndexAny(dsn, "=;")
		if i == 0 {
			return nil, errors.New("invalid odbc connection string: value without a key")
		}
		if i < 0 {
			i = len(dsn)
		}
		key := strings.ToLower(strings.TrimRightFunc(dsn[:i], unicode.IsSpace))
		if i == len(dsn) || dsn[i] == ';' {
			// A key with no value.
			setSQLServerODBCParam(kvs, key, "")
			dsn = dsn[i:]
			continue
		}
		dsn = strings.TrimLeftFunc(dsn[i+1:], unicode.IsSpace)
		var val string
		if strings.HasPrefix(dsn, "{") {
			var ok bool
			val, dsn, ok = cutODBCBraced(dsn[1:])
			if !ok {
				return nil, errors.New("invalid odbc connection string: malformed braced value")
			}
		} else {
			end := strings.IndexByte(dsn, ';')
			if end < 0 {
				end = len(dsn)
			}
			val = strings.TrimRightFunc(dsn[:end], unicode.IsSpace)
			dsn = dsn[end:]
		}
		setSQLServerODBCParam(kvs, key, val)
	}
}

// cutODBCBraced reads a braced value whose opening brace is already consumed.
// It returns the value and what follows it, and reports false when the brace
// is never closed or is followed by anything but a semicolon.
func cutODBCBraced(s string) (val, rest string, ok bool) {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '}')
		if i < 0 {
			return "", "", false
		}
		b.WriteString(s[:i])
		s = s[i+1:]
		if !strings.HasPrefix(s, "}") {
			break
		}
		b.WriteByte('}')
		s = s[1:]
	}
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	if s != "" && s[0] != ';' {
		return "", "", false
	}
	return b.String(), s, true
}
