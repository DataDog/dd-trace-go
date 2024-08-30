package main

import (
	"crypto/tls"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/envoyproxy/envoy"
	"net"
	"net/http"
	"os"

	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"
	grpctrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// AppsecCalloutExtensionService defines the struct that follows the ExternalProcessorServer interface.
type AppsecCalloutExtensionService struct {
	extproc.ExternalProcessorServer
}

func main() {
	var customService AppsecCalloutExtensionService

	tracer.Start(tracer.WithServiceName("appsec-callout-service-extension"))

	go StartGPRCSsl(&customService)
	println("gRPC server started on port 443")

	go startHealthCheck()
	println("Health check server started on port 80")

	select {}
}

func startHealthCheck() {
	muxServer := mux.NewRouter()
	muxServer.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:    "0.0.0.0:80",
		Handler: muxServer,
	}

	println(server.ListenAndServe())
}

func StartGPRCSsl(service extproc.ExternalProcessorServer) {
	certFolder := os.Getenv("CERT_FOLDER")
	if os.Getenv("CERT_FOLDER") == "" {
		certFolder = "." // Default value
	}

	cert, err := tls.LoadX509KeyPair(certFolder+"/localhost.crt", certFolder+"/localhost.key")
	if err != nil {
		println("Failed to load server certificate: %v", err)
	}

	lis, err := net.Listen("tcp", "0.0.0.0:443")
	if err != nil {
		println("Failed to listen: %v", err)
	}

	si := envoy.StreamServerInterceptor(grpctrace.WithServiceName("appsec-callout-service-extension-meta"))
	creds := credentials.NewServerTLSFromCert(&cert)
	grpcServer := grpc.NewServer(grpc.StreamInterceptor(si), grpc.Creds(creds))

	extproc.RegisterExternalProcessorServer(grpcServer, service)
	reflection.Register(grpcServer) // TODO: remove
	if err := grpcServer.Serve(lis); err != nil {
		println("Failed to serve gRPC: %v", err)
	}
}
