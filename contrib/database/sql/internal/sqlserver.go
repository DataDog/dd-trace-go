// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"fmt"
	"net"
	nurl "net/url"
	"strings"
)

func parseSqlServerURL(url string) (map[string]string, error) {
	u, err := nurl.Parse(url)
	if err != nil {
		return nil, err
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
		accrue("instanceName", u.Path[1:])
	}

	q := u.Query()
	for k := range q {
		if k == "database" {
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

func parseSqlServerADO(dsn string) (map[string]string, error) {
	kvs := map[string]string{}
	fields := strings.Split(dsn, ";")
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
		}
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
	return kvs, nil
}
