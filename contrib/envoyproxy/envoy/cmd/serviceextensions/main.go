package main

import (
	"crypto/tls"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/envoyproxy/envoy"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
	"net"
	"net/http"
	"os"

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
	extensionPort := os.Getenv("DD_SERVICE_EXTENSION_PORT")
	if extensionPort == "" {
		extensionPort = "443"
	}

	extensionHost := os.Getenv("DD_SERVICE_EXTENSION_HOST")
	if extensionHost == "" {
		extensionHost = "0.0.0.0"
	}

	healthcheckPort := os.Getenv("DD_SERVICE_EXTENSION_HEALTHCHECK_PORT")
	if healthcheckPort == "" {
		healthcheckPort = "80"
	}

	return serviceExtensionConfig{
		extensionPort:   extensionPort,
		extensionHost:   extensionHost,
		healthcheckPort: healthcheckPort,
	}
}

func main() {
	var extensionService AppsecCalloutExtensionService

	// Force set ASM as enabled only if the environment variable is not set
	// Note: If the environment variable is set to false, it should be disabled
	if os.Getenv("DD_APPSEC_ENABLED") == "" {
		if err := os.Setenv("DD_APPSEC_ENABLED", "1"); err != nil {
			log.Error("service_extension: failed to set DD_APPSEC_ENABLED environment variable: %v\n", err)
		}
	}

	// TODO: Enable ASM standalone mode when it is developed (should be done for Q4 2024)

	// Set the DD_VERSION to the current tracer version if not set
	if os.Getenv("DD_VERSION") == "" {
		if err := os.Setenv("DD_VERSION", version.Tag); err != nil {
			log.Error("service_extension: failed to set DD_VERSION environment variable: %v\n", err)
		}
	}

	config := loadConfig()

	tracer.Start()

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
		_, err := w.Write([]byte(`{"status": "ok", "library": {"language": "golang", "version": "` + version.Tag + `"}}`))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:    config.extensionHost + ":" + config.healthcheckPort,
		Handler: muxServer,
	}

	println(server.ListenAndServe())
}

func StartGPRCSsl(service extproc.ExternalProcessorServer, config serviceExtensionConfig) {
	cert, err := tls.LoadX509KeyPair("localhost.crt", "localhost.key")
	if err != nil {
		log.Error("Failed to load key pair: %v\n", err)
	}

	lis, err := net.Listen("tcp", config.extensionHost+":"+config.extensionPort)
	if err != nil {
		log.Error("Failed to listen: %v\n", err)
	}

	si := envoy.StreamServerInterceptor()
	creds := credentials.NewServerTLSFromCert(&cert)
	grpcServer := grpc.NewServer(grpc.StreamInterceptor(si), grpc.Creds(creds))

	extproc.RegisterExternalProcessorServer(grpcServer, service)
	reflection.Register(grpcServer)
	if err := grpcServer.Serve(lis); err != nil {
		log.Error("service_extension: failed to serve gRPC: %v\n", err)
	}
}
