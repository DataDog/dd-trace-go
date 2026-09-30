// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.
// Portions Copyright (c) 2026 CloudX. See LICENSE for the original MIT license.

package clickhouse

import "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

// config holds metadata applied to every span from one wrapped connection.
type config struct {
	spanConfig   *tracer.StartSpanConfig
	serviceName  string
	peerService  string
	resourceName string
	host         string
	port         string
	database     string
	user         string
	errCheck     func(error) bool
}

// Option configures spans from [Wrap]. Pass options to Wrap when constructing
// the connection. Options never change the underlying ClickHouse connection.
type Option func(*config)

// WithService sets the service name on ClickHouse spans. Without it, spans
// inherit the application's service name. An empty name keeps that default.
func WithService(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithPeerService sets the peer.service tag on ClickHouse spans. Use it to name
// the remote ClickHouse service in Datadog. An empty name omits the tag.
func WithPeerService(name string) Option {
	return func(cfg *config) {
		cfg.peerService = name
	}
}

// WithResourceName replaces the span resource with a fixed name. Use it if
// query text may contain secrets or many distinct values. An empty name uses
// the SQL query, or "Ping" for Ping.
func WithResourceName(name string) Option {
	return func(cfg *config) {
		cfg.resourceName = name
	}
}

// WithHost sets the server hostname tag on spans. Supply the host from your
// ClickHouse connection options; it is not read from the connection.
func WithHost(host string) Option {
	return func(cfg *config) {
		cfg.host = host
	}
}

// WithPort sets the server port tag on spans. Supply the port from your
// ClickHouse connection options; it is not read from the connection.
func WithPort(port string) Option {
	return func(cfg *config) {
		cfg.port = port
	}
}

// WithDatabase sets the database name tag on spans. Supply the database from
// your ClickHouse connection options; it is not read from the connection.
func WithDatabase(database string) Option {
	return func(cfg *config) {
		cfg.database = database
	}
}

// WithUser sets the database username tag on spans. Supply the user from your
// ClickHouse connection options; it is not read from the connection.
func WithUser(user string) Option {
	return func(cfg *config) {
		cfg.user = user
	}
}

// WithErrorCheck controls which driver errors mark spans as errors. The
// callback runs only for non-nil errors. Return false to suppress the error on
// the span; callers still receive the original error. A nil callback reports
// every non-nil error.
func WithErrorCheck(fn func(error) bool) Option {
	return func(cfg *config) {
		cfg.errCheck = fn
	}
}
