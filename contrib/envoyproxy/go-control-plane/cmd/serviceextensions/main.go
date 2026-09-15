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
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/credentials"

	gocontrolplane "github.com/DataDog/dd-trace-go/contrib/envoyproxy/go-control-plane/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"

	extproc "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
)

// AppsecCalloutExtensionService defines the struct that follows the ExternalProcessorServer interface.
type AppsecCalloutExtensionService struct {
	extproc.ExternalProcessorServer
}

type tlsConfig struct {
	certFile string
	keyFile  string
}

type serviceExtensionConfig struct {
	extensionPort        string
	extensionHost        string
	extensionSocketPath  string
	healthcheckPort      string
	observabilityMode    bool
	bodyParsingSizeLimit *int
	integration          string
	tls                  *tlsConfig
}

// trustGCLBXForwardedFor reports whether the published binary is running behind
// a Google Cloud load balancer. UDS is documented for self-managed Envoy, whose
// one-entry X-Forwarded-For append does not satisfy GCLB's positional contract.
func trustGCLBXForwardedFor(config serviceExtensionConfig) bool {
	return config.extensionSocketPath == ""
}

// integration reports which gateway is in front of this callout.
//
// An explicit value always wins. It has to be available, because the gateway is otherwise
// only identifiable from a per-request header, and Envoy Gateway cannot inject one from
// its EnvoyExtensionPolicy CRD. Inferring it from the presence of Kubernetes does not
// work: a Google Cloud Service Extensions callout on GKE is in Kubernetes too.
//
// Without one, a Unix socket means a self-managed Envoy on the same host, and anything
// else keeps the Google Cloud default the published image is built for.
func integration(config serviceExtensionConfig) gocontrolplane.Integration {
	switch name := strings.ToLower(strings.TrimSpace(config.integration)); name {
	case "":
	case gocontrolplane.GCPServiceExtensionIntegration.String():
		return gocontrolplane.GCPServiceExtensionIntegration
	case gocontrolplane.EnvoyIntegration.String():
		return gocontrolplane.EnvoyIntegration
	case gocontrolplane.EnvoyGatewayIntegration.String():
		return gocontrolplane.EnvoyGatewayIntegration
	case gocontrolplane.IstioIntegration.String():
		return gocontrolplane.IstioIntegration
	default:
		log.Warn("service_extension: unknown DD_SERVICE_EXTENSION_INTEGRATION value %q, falling back to autodetection\n", name)
	}

	if config.extensionSocketPath != "" {
		return gocontrolplane.EnvoyIntegration
	}

	return gocontrolplane.GCPServiceExtensionIntegration
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
		setValue := env.Get(k)
		if setValue == "" {
			if err := os.Setenv(k, v); err != nil {
				log.Error("service_extension: failed to set %s environment variable: %s\n", k, err.Error())
				continue
			}
			gocontrolplane.Instrumentation().TelemetryRegisterAppConfig(k, v, instrumentation.TelemetryOriginDefault)
			continue
		}
		gocontrolplane.Instrumentation().TelemetryRegisterAppConfig(k, setValue, instrumentation.TelemetryOriginEnvVar)
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
		return fmt.Errorf("failed to set %s environment variable: %s", internalBlockingUnavailableKey, err)
	}
	log.Debug("service_extension: observability mode enabled, disabling blocking\n")
	return nil
}

const (
	// The cgroup filesystem, and the memory limit file within a v2 and a v1 hierarchy.
	// Paths are relative so tests can resolve them against a fixture tree.
	cgroupMountPath     = "sys/fs/cgroup"
	cgroupV2MemoryLimit = "memory.max"
	cgroupV1MemoryLimit = "memory/memory.limit_in_bytes"
	procSelfCgroupPath  = "proc/self/cgroup"

	// goMemLimitRatio is the share of the container memory limit handed to the Go
	// runtime. The remainder covers what GOMEMLIMIT cannot account for: the binary's
	// own mappings, kernel memory held on our behalf, and — the reason this sits below
	// the 90% commonly used elsewhere — libddwaf's C allocations, since this binary is
	// built with cgo and that memory is invisible to the Go runtime yet still counts
	// against the cgroup.
	goMemLimitRatio = 0.85

	// cgroup v1 reports "unlimited" as a huge sentinel rather than a keyword, so treat
	// implausibly large values as absent.
	cgroupMemoryUnlimited = int64(1) << 62
)

// configureGoMemoryLimit derives GOMEMLIMIT from the container memory limit.
//
// The Go runtime does not read cgroup memory limits, in any version up to and
// including Go 1.26: with GOMEMLIMIT unset the limit is math.MaxInt64, so the garbage
// collector paces itself purely off heap growth and never learns that a ceiling
// exists. The kernel then OOM-kills the process while the runtime still believes it
// has room. Deriving the limit here turns that hard kill into GC pressure.
//
// An explicitly configured GOMEMLIMIT always wins, and an unreadable or unlimited
// cgroup leaves the runtime default untouched.
func configureGoMemoryLimit() {
	// An empty value is not an opt-out: the runtime treats GOMEMLIMIT="" exactly like an
	// unset variable and keeps math.MaxInt64, so a manifest that templates the variable
	// to an empty string would otherwise silently disable this. A non-empty value,
	// including "off", is a deliberate choice and is left alone.
	if value, ok := os.LookupEnv("GOMEMLIMIT"); ok && value != "" {
		return
	}

	limit, ok := cgroupMemoryLimit("/")
	if !ok {
		return
	}

	budget := int64(float64(limit) * goMemLimitRatio)
	if budget <= 0 {
		return
	}

	debug.SetMemoryLimit(budget)
	log.Info("service_extension: GOMEMLIMIT set to %d bytes, derived from a %d byte container memory limit\n", budget, limit)
}

// cgroupMemoryLimit reports the container memory limit in bytes, preferring cgroup v2
// over v1. root is the filesystem root to resolve the cgroup paths against.
//
// The limit is not necessarily at the root of the cgroup mount. With a private cgroup
// namespace — the common container case — the process sees its own cgroup as "/" and
// the mount root is correct. Running in the host cgroup namespace, as some Kubernetes
// runtimes still do, the mount root is the host's cgroup and reading it would report
// no limit at all or the whole machine's. /proc/self/cgroup names the path this
// process actually lives under, so try that first and walk up towards the root, since
// the limit may be set on an ancestor such as the pod rather than the container.
func cgroupMemoryLimit(root string) (int64, bool) {
	for _, limitFile := range []string{cgroupV2MemoryLimit, cgroupV1MemoryLimit} {
		mount := filepath.Join(root, cgroupMountPath)
		// The v1 memory controller is mounted in its own subdirectory, which the relative
		// cgroup path is expressed underneath.
		base := filepath.Join(mount, filepath.Dir(limitFile))
		name := filepath.Base(limitFile)

		for _, relative := range cgroupSelfPaths(root) {
			if limit, ok := readCgroupMemoryLimit(filepath.Join(base, relative, name)); ok {
				return limit, true
			}
		}
	}

	return 0, false
}

// cgroupSelfPaths returns the cgroup paths to try, from the process's own cgroup up to
// the mount root. The root is always included so a missing or unreadable
// /proc/self/cgroup degrades to the previous behaviour rather than to nothing.
func cgroupSelfPaths(root string) []string {
	content, err := os.ReadFile(filepath.Join(root, procSelfCgroupPath))
	if err != nil {
		return []string{"/"}
	}

	// Lines are "hierarchy:controllers:path"; cgroup v2 uses the single "0::<path>".
	var self string
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(fields) != 3 || fields[2] == "" {
			continue
		}
		// Prefer the v2 unified entry or the v1 memory controller; anything else describes
		// a hierarchy that does not carry the memory limit.
		if fields[0] == "0" || strings.Contains(fields[1], "memory") {
			self = fields[2]
			break
		}
	}

	paths := []string{}
	for current := self; current != "" && current != "/" && current != "."; current = filepath.Dir(current) {
		paths = append(paths, current)
	}

	return append(paths, "/")
}

// readCgroupMemoryLimit reads a single cgroup memory limit file, reporting false when
// the file is absent, unparseable, or denotes no limit.
func readCgroupMemoryLimit(path string) (int64, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}

	// cgroup v2 spells "no limit" as the literal "max".
	raw := strings.TrimSpace(string(content))
	if raw == "max" {
		return 0, false
	}

	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 || limit >= cgroupMemoryUnlimited {
		return 0, false
	}

	return limit, true
}

// logRuntimeEnvelope reports the resource envelope the process actually resolved.
//
// Every one of these is derived rather than declared — GOMEMLIMIT from the cgroup
// above, GOMAXPROCS from the cgroup CPU quota by the runtime itself since Go 1.25,
// and the body parsing limit from configuration. When a deployment runs out of
// memory, these three numbers are the first thing worth knowing, and they are
// otherwise invisible from outside the container.
func logRuntimeEnvelope(config serviceExtensionConfig) {
	// Only report a number when one was actually configured. Left unset, the limit is
	// resolved per request from the gateway integration — and GCP Service Extensions
	// resolves it to 0, disabling body parsing entirely — so printing the proxy-wide
	// default here would misreport the primary deployment this diagnostic exists for.
	bodyParsingSizeLimit := "unset (resolved per gateway integration)"
	if config.bodyParsingSizeLimit != nil {
		bodyParsingSizeLimit = strconv.Itoa(*config.bodyParsingSizeLimit) + " bytes"
	}

	// A negative argument retrieves the limit without adjusting it.
	log.Info("service_extension: runtime envelope: GOMEMLIMIT=%d bytes, GOMAXPROCS=%d, body parsing size limit=%s\n",
		debug.SetMemoryLimit(-1), runtime.GOMAXPROCS(0), bodyParsingSizeLimit)
}

// loadConfig loads the configuration from the environment variables
func loadConfig() serviceExtensionConfig {
	extensionPortInt := intEnv("DD_SERVICE_EXTENSION_PORT", 443)
	healthcheckPortInt := intEnv("DD_SERVICE_EXTENSION_HEALTHCHECK_PORT", 80)
	extensionHostStr := ipEnv("DD_SERVICE_EXTENSION_HOST", net.IP{0, 0, 0, 0}).String()
	observabilityMode := boolEnv("DD_SERVICE_EXTENSION_OBSERVABILITY_MODE", false)
	bodyParsingSizeLimit := intEnvNil("DD_APPSEC_BODY_PARSING_SIZE_LIMIT")
	enableTLS := boolEnv("DD_SERVICE_EXTENSION_TLS", true)
	keyFile := stringEnv("DD_SERVICE_EXTENSION_TLS_KEY_FILE", "localhost.key")
	certFile := stringEnv("DD_SERVICE_EXTENSION_TLS_CERT_FILE", "localhost.crt")
	socketPath := stringEnv("DD_SERVICE_EXTENSION_UDS_PATH", "")
	integrationName := stringEnv("DD_SERVICE_EXTENSION_INTEGRATION", "")

	extensionPortStr := strconv.FormatInt(int64(extensionPortInt), 10)
	healthcheckPortStr := strconv.FormatInt(int64(healthcheckPortInt), 10)

	var tlsConf *tlsConfig
	if enableTLS {
		tlsConf = &tlsConfig{
			certFile: certFile,
			keyFile:  keyFile,
		}
	}

	return serviceExtensionConfig{
		extensionPort:        extensionPortStr,
		extensionHost:        extensionHostStr,
		extensionSocketPath:  socketPath,
		healthcheckPort:      healthcheckPortStr,
		observabilityMode:    observabilityMode,
		bodyParsingSizeLimit: bodyParsingSizeLimit,
		integration:          integrationName,
		tls:                  tlsConf,
	}
}

func main() {
	initializeEnvironment()
	configureGoMemoryLimit()

	config := loadConfig()
	logRuntimeEnvelope(config)

	if err := configureObservabilityMode(config.observabilityMode); err != nil {
		log.Error("service_extension: %s\n", err.Error())
	}

	if err := startService(config); err != nil {
		log.Error("service_extension: %s\n", err.Error())
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

	return g.Wait()
}

func startHealthCheck(ctx context.Context, config serviceExtensionConfig) error {
	imageVersion := stringEnv("DD_VERSION", instrumentation.Version())
	muxServer := http.NewServeMux()
	muxServer.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok", "library": {"language": "golang", "version": "` + imageVersion + `"}}`))
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
		return fmt.Errorf("health check http server: %s", err)
	}

	return nil
}

func startGPRCSsl(ctx context.Context, service extproc.ExternalProcessorServer, config serviceExtensionConfig) error {
	var (
		lis net.Listener
		err error
	)

	if config.extensionSocketPath != "" {
		// Unix domain socket mode: remove any stale socket file before binding.
		if removeErr := os.Remove(config.extensionSocketPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			log.Warn("service_extension: could not remove stale socket file %s: %s\n", config.extensionSocketPath, removeErr)
		}
		lis, err = net.Listen("unix", config.extensionSocketPath)
		if err != nil {
			return fmt.Errorf("gRPC server: %s", err)
		}
		log.Info("service_extension: callout gRPC server started on unix://%s\n", config.extensionSocketPath)
	} else {
		lis, err = net.Listen("tcp", config.extensionHost+":"+config.extensionPort)
		if err != nil {
			return fmt.Errorf("gRPC server: %s", err)
		}
		log.Info("service_extension: callout gRPC server started on %s:%s\n", config.extensionHost, config.extensionPort)
	}

	var serverOptions []grpc.ServerOption

	if config.tls != nil && config.extensionSocketPath == "" {
		cert, err := tls.LoadX509KeyPair(config.tls.certFile, config.tls.keyFile)
		if err != nil {
			return fmt.Errorf("failed to load key pair: %s", err)
		}
		serverOptions = append(serverOptions, grpc.Creds(credentials.NewServerTLSFromCert(&cert)))
		log.Info("service_extension: TLS enabled for gRPC server")
		log.Info("service_extension: TLS key file path: %s\n", config.tls.keyFile)
		log.Info("service_extension: TLS cert file path: %s\n", config.tls.certFile)
	}

	grpcServer := grpc.NewServer(serverOptions...)
	appsecEnvoyExternalProcessorServer := gocontrolplane.AppsecEnvoyExternalProcessorServer(
		service,
		gocontrolplane.AppsecEnvoyConfig{
			Integration:            integration(config),
			TrustGCLBXForwardedFor: trustGCLBXForwardedFor(config),
			BlockingUnavailable:    config.observabilityMode,
			Context:                ctx,
			BodyParsingSizeLimit:   config.bodyParsingSizeLimit,
		})

	if config.extensionSocketPath != "" {
		defer os.Remove(config.extensionSocketPath)
	}

	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	extproc.RegisterExternalProcessorServer(grpcServer, appsecEnvoyExternalProcessorServer)
	if err := grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("error starting gRPC server: %s", err)
	}

	return nil
}
