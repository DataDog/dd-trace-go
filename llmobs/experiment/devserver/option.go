// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package devserver

// Option configures a Server.
type Option func(cfg *serverCfg)

type serverCfg struct {
	addr        string
	corsOrigins []string
}

func defaultServerCfg() *serverCfg {
	return &serverCfg{
		addr:        ":8787",
		corsOrigins: []string{"*"},
	}
}

// WithAddr sets the listen address for the server.
func WithAddr(addr string) Option {
	return func(cfg *serverCfg) {
		cfg.addr = addr
	}
}

// WithCORSOrigins sets the allowed CORS origins. Default is ["*"].
func WithCORSOrigins(origins ...string) Option {
	return func(cfg *serverCfg) {
		cfg.corsOrigins = origins
	}
}
