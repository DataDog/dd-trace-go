// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/negasus/haproxy-spoe-go/agent"
	"golang.org/x/sync/errgroup"

	"github.com/DataDog/dd-trace-go/contrib/haproxy/stream-processing-offload/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type haProxySpoaConfig struct {
	extensionPort        string
	healthcheckPort      string
	extensionHost        string
	bodyParsingSizeLimit int
}

var log = NewLogger()

func getDefaultEnvVars() map[string]string {
	return map[string]string{
		"DD_VERSION":                   instrumentation.Version(), // Version of the tracer
		"DD_APM_TRACING_ENABLED":       "false",                   // Appsec Standalone
		"DD_APPSEC_WAF_TIMEOUT":        "10ms",                    // Proxy specific WAF timeout
		"_DD_APPSEC_PROXY_ENVIRONMENT": "true",                    // Internal config: Enable API Security proxy sampler
	}
}

// initializeEnvironment sets up required environment variables with their defaults
func initializeEnvironment() {
	for k, v := range getDefaultEnvVars() {
		if os.Getenv(k) == "" {
			if err := os.Setenv(k, v); err != nil {
				log.Error("haproxy_spoa: failed to set %s environment variable: %s\n", k, err.Error())
			}
		}
	}
}

// loadConfig loads the configuration from the environment variables
func loadConfig() haProxySpoaConfig {
	extensionHostStr := ipEnv("DD_HAPROXY_SPOA_HOST", net.IP{0, 0, 0, 0}).String()
	extensionPortInt := intEnv("DD_HAPROXY_SPOA_PORT", 3000)
	healthcheckPortInt := intEnv("DD_HAPROXY_SPOA_HEALTHCHECK_PORT", 3080)
	bodyParsingSizeLimit := intEnv("DD_APPSEC_BODY_PARSING_SIZE_LIMIT", 0)

	extensionPortStr := strconv.FormatInt(int64(extensionPortInt), 10)
	healthcheckPortStr := strconv.FormatInt(int64(healthcheckPortInt), 10)

	return haProxySpoaConfig{
		extensionPort:        extensionPortStr,
		extensionHost:        extensionHostStr,
		healthcheckPort:      healthcheckPortStr,
		bodyParsingSizeLimit: bodyParsingSizeLimit,
	}
}

func main() {
	initializeEnvironment()
	config := loadConfig()

	if err := startService(config); err != nil {
		log.Error("haproxy_spoa: %s\n", err.Error())
		os.Exit(1)
	}

	log.Info("haproxy_spoa: shutting down\n")
}

func startService(config haProxySpoaConfig) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return startSpoa(ctx, config)
	})

	g.Go(func() error {
		return startHealthCheck(ctx, config)
	})

	if err := g.Wait(); err != nil {
		return err
	}

	return nil
}

func startHealthCheck(ctx context.Context, config haProxySpoaConfig) error {
	muxServer := http.NewServeMux()
	muxServer.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok", "library": {"language": "golang", "version": "` + instrumentation.Version() + `"}}`))
	})

	server := &http.Server{
		Addr:    config.extensionHost + ":" + config.healthcheckPort,
		Handler: muxServer,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Error("haproxy_spoa: health check server shutdown: %s\n", err.Error())
		}
	}()

	log.Info("haproxy_spoa: health check server started on %s:%s\n", config.extensionHost, config.healthcheckPort)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("health check http server: %s", err.Error())
	}

	return nil
}

func startSpoa(ctx context.Context, config haProxySpoaConfig) error {
	listener, err := net.Listen("tcp4", config.extensionHost+":"+config.extensionPort)
	if err != nil {
		return fmt.Errorf("error creating listener: %w", err)
	}
	defer listener.Close()

	_ = tracer.Start(tracer.WithAppSecEnabled(true))
	defer tracer.Stop()

	appsecHAProxy := streamprocessingoffload.NewHAProxySPOA(streamprocessingoffload.AppsecHAProxyConfig{
		BlockingUnavailable:  false,
		BodyParsingSizeLimit: config.bodyParsingSizeLimit,
		Context:              ctx,
	})

	a := agent.New(appsecHAProxy.Handler, log)

	log.Info("haproxy_spoa: datadog agent server started on %s:%s\n", config.extensionHost, config.extensionPort)
	if err := a.Serve(listener); err != nil {
		return fmt.Errorf("error starting agent server: %w", err)
	}

	return nil
}
