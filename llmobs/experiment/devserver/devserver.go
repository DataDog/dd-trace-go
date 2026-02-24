// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package devserver provides an HTTP server for interactive experiment execution.
// It exposes registered experiments via /list and /eval endpoints,
// supporting real-time streaming of progress events.
//
// Handlers are exposed as composable http.Handler values that can be mounted
// into any Go HTTP framework (net/http, chi, Rapid, etc.).
package devserver

import (
	"context"
	"maps"
	"net/http"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
	"github.com/DataDog/dd-trace-go/v2/llmobs/experiment"
)

// ExperimentDefinition describes an experiment available to the server.
type ExperimentDefinition struct {
	Name              string
	Description       string
	ProjectName       string
	Task              experiment.Task
	Dataset           *dataset.Dataset
	Evaluators        []experiment.Evaluator
	SummaryEvaluators []experiment.SummaryEvaluator
	DefaultConfig     map[string]any
	Tags              map[string]string
}

// Registry holds registered experiment definitions.
// It is safe for concurrent use.
type Registry struct {
	mu          sync.RWMutex
	experiments map[string]*ExperimentDefinition
}

// NewRegistry creates a new Registry from the given definitions.
func NewRegistry(defs []*ExperimentDefinition) *Registry {
	exps := make(map[string]*ExperimentDefinition, len(defs))
	for _, def := range defs {
		exps[def.Name] = def
	}
	return &Registry{experiments: exps}
}

// Get returns an experiment definition by name.
func (r *Registry) Get(name string) (*ExperimentDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.experiments[name]
	return def, ok
}

// List returns all registered experiment definitions.
func (r *Registry) List() map[string]*ExperimentDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]*ExperimentDefinition, len(r.experiments))
	maps.Copy(result, r.experiments)
	return result
}

// Server is a convenience wrapper that combines handlers into a standalone HTTP server
// with CORS support. For embedding into other frameworks, use NewListHandler and
// NewEvalHandler directly with a Registry.
type Server struct {
	registry *Registry
	cfg      *serverCfg
	handler  http.Handler
}

// New creates a new Server from a list of experiment definitions and options.
func New(defs []*ExperimentDefinition, opts ...Option) *Server {
	cfg := defaultServerCfg()
	for _, opt := range opts {
		opt(cfg)
	}
	registry := NewRegistry(defs)
	mux := http.NewServeMux()
	mux.Handle("GET /list", NewListHandler(registry))
	mux.Handle("POST /eval", NewEvalHandler(registry))

	handler := corsMiddleware(mux, cfg.corsOrigins)
	return &Server{
		registry: registry,
		cfg:      cfg,
		handler:  handler,
	}
}

// Handler returns the server's http.Handler for embedding into another server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// ListenAndServe starts the HTTP server and blocks until the context is cancelled.
// It performs a graceful shutdown when the context is done.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:    s.cfg.addr,
		Handler: s.handler,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		return srv.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}
