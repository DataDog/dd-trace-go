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

	gocontrolplane "github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// AppsecCalloutExtensionService defines the struct that follows the ExternalProcessorServer interface.
type AppsecCalloutExtensionService struct {
	extproc.ExternalProcessorServer
}

type serviceExtensionConfig struct {
	extensionPort   string
	extensionHost   string
	healthcheckPort string
}

func loadConfig() serviceExtensionConfig {
	extensionPortInt := intEnv("DD_SERVICE_EXTENSION_PORT", 443)
	healthcheckPortInt := intEnv("DD_SERVICE_EXTENSION_HEALTHCHECK_PORT", 80)
	extensionHostStr := ipEnv("DD_SERVICE_EXTENSION_HOST", net.IP{0, 0, 0, 0}).String()

	extensionPortStr := strconv.FormatInt(int64(extensionPortInt), 10)
	healthcheckPortStr := strconv.FormatInt(int64(healthcheckPortInt), 10)

	return serviceExtensionConfig{
		extensionPort:   extensionPortStr,
		extensionHost:   extensionHostStr,
		healthcheckPort: healthcheckPortStr,
	}
}

var log = NewLogger()

func main() {
	// Set the DD_VERSION to the current tracer version if not set
	if os.Getenv("DD_VERSION") == "" {
		if err := os.Setenv("DD_VERSION", version); err != nil {
			log.Error("service_extension: failed to set DD_VERSION environment variable: %v\n", err)
		}
	}

	config := loadConfig()

	if err := startService(config); err != nil {
		log.Error("service_extension: %v\n", err)
		os.Exit(1)
	}

	log.Info("service_extension: shutting down\n")
}

func startService(config serviceExtensionConfig) error {
	var extensionService AppsecCalloutExtensionService

	tracer.Start(tracer.WithAppSecEnabled(true))
	defer tracer.Stop()
	// TODO: Enable ASM standalone mode when it is developed (should be done for Q4 2024)

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
	muxServer := mux.NewRouter()
	muxServer.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok", "library": {"language": "golang", "version": "` + version + `"}}`))
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
			log.Error("service_extension: health check server shutdown: %v\n", err)
		}
	}()

	log.Info("service_extension: health check server started on %s:%s\n", config.extensionHost, config.healthcheckPort)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("health check http server: %v", err)
	}

	return nil
}

func startGPRCSsl(ctx context.Context, service extproc.ExternalProcessorServer, config serviceExtensionConfig) error {
	cert, err := tls.LoadX509KeyPair("localhost.crt", "localhost.key")
	if err != nil {
		return fmt.Errorf("failed to load key pair: %v", err)
	}

	lis, err := net.Listen("tcp", config.extensionHost+":"+config.extensionPort)
	if err != nil {
		return fmt.Errorf("gRPC server: %v", err)
	}

	grpcCredentials := credentials.NewServerTLSFromCert(&cert)
	grpcServer := grpc.NewServer(grpc.Creds(grpcCredentials))

	appsecEnvoyExternalProcessorServer := gocontrolplane.AppsecEnvoyExternalProcessorServerGCPServiceExtension(service)

	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	extproc.RegisterExternalProcessorServer(grpcServer, appsecEnvoyExternalProcessorServer)
	log.Info("service_extension: callout gRPC server started on %s:%s\n", config.extensionHost, config.extensionPort)
	if err := grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("error starting gRPC server: %v", err)
	}

	return nil
}
