// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/credentials"

	gocontrolplane "github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
)

// AppsecCalloutExtensionService defines the struct that follows the ExternalProcessorServer interface.
type AppsecCalloutExtensionService struct {
	extproc.ExternalProcessorServer
}

type serviceExtensionConfig struct {
	extensionPort        string
	extensionHost        string
	healthcheckPort      string
	observabilityMode    bool
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
				log.Error("service_extension: failed to set %s environment variable: %s\n", k, err.Error())
			}
		}
	}
}

// configureObservabilityMode disables blocking when observability mode is enabled.
// Note: This requires the Envoy configuration option "observability_mode: true" to be set.
// This option is only supported when configuring Envoy directly, and is not available when using GCP Service Extension.
func configureObservabilityMode(mode bool) error {
	if !mode {
		return nil
	}
	const internalBlockingUnavailableKey = "_DD_APPSEC_BLOCKING_UNAVAILABLE"
	if err := os.Setenv(internalBlockingUnavailableKey, "true"); err != nil {
		return fmt.Errorf("failed to set %s environment variable: %s", internalBlockingUnavailableKey, err.Error())
	}
	log.Debug("service_extension: observability mode enabled, disabling blocking\n")
	return nil
}

// loadConfig loads the configuration from the environment variables
func loadConfig() serviceExtensionConfig {
	extensionPortInt := intEnv("DD_SERVICE_EXTENSION_PORT", 443)
	healthcheckPortInt := intEnv("DD_SERVICE_EXTENSION_HEALTHCHECK_PORT", 80)
	extensionHostStr := ipEnv("DD_SERVICE_EXTENSION_HOST", net.IP{0, 0, 0, 0}).String()
	observabilityMode := boolEnv("DD_SERVICE_EXTENSION_OBSERVABILITY_MODE", false)
	bodyParsingSizeLimit := intEnv("DD_APPSEC_BODY_PARSING_SIZE_LIMIT", 0)

	extensionPortStr := strconv.FormatInt(int64(extensionPortInt), 10)
	healthcheckPortStr := strconv.FormatInt(int64(healthcheckPortInt), 10)

	return serviceExtensionConfig{
		extensionPort:        extensionPortStr,
		extensionHost:        extensionHostStr,
		healthcheckPort:      healthcheckPortStr,
		observabilityMode:    observabilityMode,
		bodyParsingSizeLimit: bodyParsingSizeLimit,
	}
}

func main() {
	initializeEnvironment()
	config := loadConfig()

	if err := configureObservabilityMode(config.observabilityMode); err != nil {
		log.Error("service_extension: %s\n", err.Error())
	}

	if err := startService(config); err != nil {
		log.Error("service_extension: %s\n", err.Error())
		os.Exit(1)
	}

	log.Info("service_extension: shutting down\n")
}

func startService(config serviceExtensionConfig) error {
	var extensionService AppsecCalloutExtensionService

	tracer.Start(tracer.WithAppSecEnabled(true))
	defer tracer.Stop()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return startGPRCSsl(ctx, &extensionService, config)
	})

	g.Go(func() error {
		return startHealthCheck(ctx, config)
	})

	if err := g.Wait(); err != nil {
		return err
	}

	return nil
}

func startHealthCheck(ctx context.Context, config serviceExtensionConfig) error {
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
			log.Error("service_extension: health check server shutdown: %s\n", err.Error())
		}
	}()

	log.Info("service_extension: health check server started on %s:%s\n", config.extensionHost, config.healthcheckPort)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("health check http server: %s", err.Error())
	}

	return nil
}

func startGPRCSsl(ctx context.Context, service extproc.ExternalProcessorServer, config serviceExtensionConfig) error {
	lis, err := net.Listen("tcp", config.extensionHost+":"+config.extensionPort)
	if err != nil {
		return fmt.Errorf("gRPC server: %s", err.Error())
	}

	cert, err := tls.LoadX509KeyPair("localhost.crt", "localhost.key")
	if err != nil {
		return fmt.Errorf("failed to load key pair: %s", err.Error())
	}

	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewServerTLSFromCert(&cert)))
	appsecEnvoyExternalProcessorServer := gocontrolplane.AppsecEnvoyExternalProcessorServer(
		service,
		gocontrolplane.AppsecEnvoyConfig{
			Integration:          gocontrolplane.GCPServiceExtensionIntegration,
			BlockingUnavailable:  config.observabilityMode,
			Context:              ctx,
			BodyParsingSizeLimit: config.bodyParsingSizeLimit,
		})

	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	extproc.RegisterExternalProcessorServer(grpcServer, appsecEnvoyExternalProcessorServer)
	log.Info("service_extension: callout gRPC server started on %s:%s\n", config.extensionHost, config.extensionPort)
	if err := grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("error starting gRPC server: %s", err.Error())
	}

	return nil
}
