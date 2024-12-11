// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package main

import (
	"crypto/tls"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"net"
	"net/http"
	"os"
	"strconv"

	gocontrolplane "gopkg.in/DataDog/dd-trace-go.v1/contrib/envoyproxy/go-control-plane"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
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
	extensionPortInt := internal.IntEnv("DD_SERVICE_EXTENSION_PORT", 443)
	if extensionPortInt < 1 || extensionPortInt > 65535 {
		log.Error("service_extension: invalid port number: %d\n", extensionPortInt)
		os.Exit(1)
	}

	healthcheckPortInt := internal.IntEnv("DD_SERVICE_EXTENSION_HEALTHCHECK_PORT", 80)
	if healthcheckPortInt < 1 || healthcheckPortInt > 65535 {
		log.Error("service_extension: invalid port number: %d\n", healthcheckPortInt)
		os.Exit(1)
	}

	extensionHost := internal.IpEnv("DD_SERVICE_EXTENSION_HOST", "0.0.0.0")
	extensionPortStr := strconv.FormatInt(int64(extensionPortInt), 10)
	healthcheckPortStr := strconv.FormatInt(int64(healthcheckPortInt), 10)

	// check if the ports are free
	l, err := net.Listen("tcp", extensionHost+":"+extensionPortStr)
	if err != nil {
		log.Error("service_extension: failed to listen on extension %s:%s: %v\n", extensionHost, extensionPortStr, err)
		os.Exit(1)
	}
	err = l.Close()
	if err != nil {
		log.Error("service_extension: failed to close listener on %s:%s: %v\n", extensionHost, extensionPortStr, err)
		os.Exit(1)
	}

	l, err = net.Listen("tcp", extensionHost+":"+healthcheckPortStr)
	if err != nil {
		log.Error("service_extension: failed to listen on health check %s:%s: %v\n", extensionHost, healthcheckPortStr, err)
		os.Exit(1)
	}
	err = l.Close()
	if err != nil {
		log.Error("service_extension: failed to close listener on %s:%s: %v\n", extensionHost, healthcheckPortStr, err)
		os.Exit(1)
	}

	return serviceExtensionConfig{
		extensionPort:   extensionPortStr,
		extensionHost:   extensionHost,
		healthcheckPort: healthcheckPortStr,
	}
}

func main() {
	var extensionService AppsecCalloutExtensionService

	// Set the DD_VERSION to the current tracer version if not set
	if os.Getenv("DD_VERSION") == "" {
		if err := os.Setenv("DD_VERSION", version.Tag); err != nil {
			log.Error("service_extension: failed to set DD_VERSION environment variable: %v\n", err)
		}
	}

	config := loadConfig()

	tracer.Start(tracer.WithAppSecEnabled(true))
	// TODO: Enable ASM standalone mode when it is developed (should be done for Q4 2024)

	go StartGPRCSsl(&extensionService, config)
	log.Info("service_extension: callout gRPC server started on %s:%s\n", config.extensionHost, config.extensionPort)

	go startHealthCheck(config)
	log.Info("service_extension: health check server started on %s:%s\n", config.extensionHost, config.healthcheckPort)

	select {}
}

func startHealthCheck(config serviceExtensionConfig) {
	muxServer := mux.NewRouter()
	muxServer.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok", "library": {"language": "golang", "version": "` + version.Tag + `"}}`))
	})

	server := &http.Server{
		Addr:    config.extensionHost + ":" + config.healthcheckPort,
		Handler: muxServer,
	}

	if err := server.ListenAndServe(); err != nil {
		log.Error("service_extension: error starting health check http server: %v\n", err)
	}
}

func StartGPRCSsl(service extproc.ExternalProcessorServer, config serviceExtensionConfig) {
	cert, err := tls.LoadX509KeyPair("localhost.crt", "localhost.key")
	if err != nil {
		log.Error("service_extension: failed to load key pair: %v\n", err)
		os.Exit(1)
		return
	}

	lis, err := net.Listen("tcp", config.extensionHost+":"+config.extensionPort)
	if err != nil {
		log.Error("service_extension: gRPC server failed to listen: %v\n", err)
		os.Exit(1)
		return
	}

	grpcCredentials := credentials.NewServerTLSFromCert(&cert)
	grpcServer := grpc.NewServer(grpc.Creds(grpcCredentials))

	appsecEnvoyExternalProcessorServer := gocontrolplane.AppsecEnvoyExternalProcessorServer(service)

	extproc.RegisterExternalProcessorServer(grpcServer, appsecEnvoyExternalProcessorServer)
	reflection.Register(grpcServer)
	if err := grpcServer.Serve(lis); err != nil {
		log.Error("service_extension: error starting gRPC server: %v\n", err)
		os.Exit(1)
	}
}
