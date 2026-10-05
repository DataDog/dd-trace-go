// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	apimcallout "github.com/DataDog/dd-trace-go/contrib/azure/apim-callout/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
)

type config struct {
	port           string
	host           string
	healthPort     string
	bodyLimit      int
	requestTimeout time.Duration
	tlsEnabled     bool
	tlsCertFile    string
	tlsKeyFile     string
}

var log = NewLogger()

func getDefaultEnvVars() map[string]string {
	return map[string]string{
		"DD_VERSION":                   instrumentation.Version(),
		"DD_APM_TRACING_ENABLED":       "false",
		"DD_APPSEC_WAF_TIMEOUT":        "10ms",
		"_DD_APPSEC_PROXY_ENVIRONMENT": "true",
		"DD_TRACE_PROPAGATION_STYLE":   "datadog",
	}
}

// initializeEnvironment sets up required environment variables with their defaults
func initializeEnvironment() {
	for k, v := range getDefaultEnvVars() {
		setValue := env.Get(k)
		if setValue == "" {
			if err := os.Setenv(k, v); err != nil {
				log.Error("apim_callout: failed to set %s environment variable: %s\n", k, err.Error())
				continue
			}
			apimcallout.Instrumentation().TelemetryRegisterAppConfig(k, v, instrumentation.TelemetryOriginDefault)
			continue
		}
		apimcallout.Instrumentation().TelemetryRegisterAppConfig(k, setValue, instrumentation.TelemetryOriginEnvVar)
	}
}

// loadConfig loads the configuration from the environment variables.
func loadConfig() config {
	return config{
		host:           ipEnv("DD_APIM_CALLOUT_HOST", net.IP{0, 0, 0, 0}).String(),
		port:           strconv.Itoa(intEnv("DD_APIM_CALLOUT_PORT", 8080)),
		healthPort:     strconv.Itoa(intEnv("DD_APIM_CALLOUT_HEALTHCHECK_PORT", 8081)),
		bodyLimit:      intEnv("DD_APPSEC_BODY_PARSING_SIZE_LIMIT", proxy.DefaultBodyParsingSizeLimit),
		requestTimeout: durationEnv("DD_APIM_CALLOUT_REQUEST_TIMEOUT", 30*time.Second),
		tlsEnabled:     boolEnv("DD_APIM_CALLOUT_TLS", false),
		tlsCertFile:    stringEnv("DD_APIM_CALLOUT_TLS_CERT_FILE", ""),
		tlsKeyFile:     stringEnv("DD_APIM_CALLOUT_TLS_KEY_FILE", ""),
	}
}

func main() {
	initializeEnvironment()
	config := loadConfig()

	if err := startService(config); err != nil {
		log.Error("apim_callout: %s\n", err.Error())
		os.Exit(1)
	}

	log.Info("apim_callout: shutting down\n")
}

func startService(config config) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		if err := startHealthCheck(ctx, config); err != nil && ctx.Err() == nil {
			log.Error("apim_callout: health check failed: %s\n", err.Error())
			cancel()
		}
	}()

	return startCalloutServer(ctx, config)
}

func startHealthCheck(ctx context.Context, config config) error {
	imageVersion := stringEnv("DD_VERSION", instrumentation.Version())
	healthResp, err := json.Marshal(map[string]any{
		"status":  "ok",
		"library": map[string]string{"language": "golang", "version": imageVersion},
	})
	if err != nil {
		return fmt.Errorf("marshal health response: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(healthResp)
	})

	server := &http.Server{
		Addr:    config.host + ":" + config.healthPort,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Error("apim_callout: health check server shutdown: %s\n", err.Error())
		}
	}()

	log.Info("apim_callout: health check server started on %s:%s\n", config.host, config.healthPort)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("health check http server: %w", err)
	}

	return nil
}

func startCalloutServer(ctx context.Context, config config) error {
	if err := tracer.Start(tracer.WithAppSecEnabled(true)); err != nil {
		return fmt.Errorf("failed to start tracer with appsec: %w", err)
	}
	defer tracer.Stop()

	bodyLimit := config.bodyLimit
	handler := apimcallout.NewHandler(apimcallout.AppsecAPIMConfig{
		Context:              ctx,
		BlockingUnavailable:  false,
		BodyParsingSizeLimit: &bodyLimit,
		RequestTimeout:       config.requestTimeout,
	})

	addr := config.host + ":" + config.port
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Error("apim_callout: server shutdown: %s\n", err.Error())
		}
	}()

	if config.tlsEnabled {
		if config.tlsCertFile == "" || config.tlsKeyFile == "" {
			return fmt.Errorf("TLS enabled but DD_APIM_CALLOUT_TLS_CERT_FILE and DD_APIM_CALLOUT_TLS_KEY_FILE must be set")
		}
		server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		log.Info("apim_callout: HTTPS server started on %s\n", addr)
		if err := server.ListenAndServeTLS(config.tlsCertFile, config.tlsKeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("https server: %w", err)
		}
	} else {
		log.Info("apim_callout: HTTP server started on %s\n", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
	}

	return nil
}
