// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package main is the dd-trace-go Dagger module: one Function per CI script
// or job, built on the pinned base container under ../base/<goVersion>. The
// same Function runs on a laptop and in a GitHub Actions step, so the test
// environment has one definition, not a workflow-YAML copy that can drift
// from it.
package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"dagger/ci/internal/dagger"

	"gopkg.in/yaml.v3"
)

type Ci struct{}

// defaultPlatform matches GitHub-hosted runners, which are amd64. Some
// pinned images, including the older Elasticsearch versions, have no arm64
// manifest and fail to resolve under any other default.
const defaultPlatform = dagger.Platform("linux/amd64")

func resolvePlatform(platform string) dagger.Platform {
	if platform == "" {
		return defaultPlatform
	}
	return dagger.Platform(platform)
}

// Base returns the pinned container for Go version "1.26" or "1.27", built
// from ../base/<goVersion>/Dockerfile. It carries no repository source, so
// the same image serves a fresh CI checkout and an uncommitted local
// working tree without rebuilding.
//
// platform defaults to linux/amd64. A native arm64 build skips emulation
// but no longer matches GitHub-hosted runners.
func (m *Ci) Base(source *dagger.Directory, goVersion string,
	// +optional
	platform string,
) (*dagger.Container, error) {
	return source.DockerBuild(dagger.DirectoryDockerBuildOpts{
		Dockerfile: fmt.Sprintf(".dagger/base/go%s/Dockerfile", goVersion),
		Platform:   resolvePlatform(platform),
	}), nil
}

func (m *Ci) GoVersion(ctx context.Context, source *dagger.Directory, goVersion string,
	// +optional
	platform string,
) (string, error) {
	base, err := m.Base(source, goVersion, platform)
	if err != nil {
		return "", err
	}
	return base.WithExec([]string{"go", "version"}).Stdout(ctx)
}

// composePort mirrors one docker-compose "hostPort:containerPort[/proto]"
// mapping.
type composePort struct {
	hostPort      int // the port dd-trace-go tests dial on 127.0.0.1
	containerPort int // the port the service image listens on
	protocol      dagger.NetworkProtocol
}

// composeService mirrors one entry in .github/testservices/docker-compose.yaml.
type composeService struct {
	image string
	env   map[string]string
	ports []composePort
}

// composeFile mirrors the subset of docker-compose schema
// .github/testservices/docker-compose.yaml actually uses: environment as a
// map, ports as "host:container[/proto]" strings. It does not handle any
// other compose syntax (list-style environment, long-form ports, ...).
type composeFile struct {
	Services map[string]struct {
		Image       string            `yaml:"image"`
		Environment map[string]string `yaml:"environment"`
		Ports       []string          `yaml:"ports"`
	} `yaml:"services"`
}

const composeFilePath = ".github/testservices/docker-compose.yaml"

// loadComposeServices parses composeFilePath out of source, keyed by service
// alias, so that file stays the only place a service's image, environment,
// or port changes.
func loadComposeServices(ctx context.Context, source *dagger.Directory) (map[string]composeService, error) {
	contents, err := source.File(composeFilePath).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", composeFilePath, err)
	}
	var file composeFile
	if err := yaml.Unmarshal([]byte(contents), &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", composeFilePath, err)
	}
	services := make(map[string]composeService, len(file.Services))
	for alias, svc := range file.Services {
		ports := make([]composePort, len(svc.Ports))
		for i, raw := range svc.Ports {
			port, err := parseComposePort(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: %s: %w", composeFilePath, alias, err)
			}
			ports[i] = port
		}
		services[alias] = composeService{image: svc.Image, env: svc.Environment, ports: ports}
	}
	return services, nil
}

// parseComposePort parses a compose "host:container[/proto]" port mapping.
// proto defaults to tcp, matching compose's own default.
func parseComposePort(raw string) (composePort, error) {
	proto := "tcp"
	if hostContainer, p, ok := strings.Cut(raw, "/"); ok {
		raw, proto = hostContainer, p
	}
	hostStr, containerStr, ok := strings.Cut(raw, ":")
	if !ok {
		return composePort{}, fmt.Errorf("port %q has no host:container separator", raw)
	}
	host, err := strconv.Atoi(hostStr)
	if err != nil {
		return composePort{}, fmt.Errorf("port %q: %w", raw, err)
	}
	container, err := strconv.Atoi(containerStr)
	if err != nil {
		return composePort{}, fmt.Errorf("port %q: %w", raw, err)
	}
	var protocol dagger.NetworkProtocol
	switch proto {
	case "tcp":
		protocol = dagger.NetworkProtocolTcp
	case "udp":
		protocol = dagger.NetworkProtocolUdp
	default:
		return composePort{}, fmt.Errorf("port %q: unsupported protocol %q", raw, proto)
	}
	return composePort{hostPort: host, containerPort: container, protocol: protocol}, nil
}

// bindServices attaches the named compose services to c and returns a shell
// snippet that forwards each service's published port from the container's
// own loopback to its Dagger alias.
func bindServices(services map[string]composeService, c *dagger.Container, platform dagger.Platform, aliases ...string) (*dagger.Container, string) {
	var forwards strings.Builder
	for _, alias := range aliases {
		svcDef, ok := services[alias]
		if !ok {
			panic(fmt.Sprintf("no compose service named %q", alias))
		}
		svc := dag.Container(dagger.ContainerOpts{Platform: platform}).From(svcDef.image).WithoutDockerHealthcheck()
		for name, value := range svcDef.env {
			svc = svc.WithEnvVariable(name, value)
		}
		for _, p := range svcDef.ports {
			svc = svc.WithExposedPort(p.containerPort, dagger.ContainerWithExposedPortOpts{
				Protocol:                    p.protocol,
				ExperimentalSkipHealthcheck: true,
			})
		}
		c = c.WithServiceBinding(alias, svc.AsService())
		for _, p := range svcDef.ports {
			if p.protocol == dagger.NetworkProtocolUdp {
				fmt.Fprintf(&forwards, "socat UDP-LISTEN:%d,fork,reuseaddr UDP:%s:%d &\n", p.hostPort, alias, p.containerPort)
				continue
			}
			fmt.Fprintf(&forwards, "socat TCP-LISTEN:%d,fork,reuseaddr TCP:%s:%d &\n", p.hostPort, alias, p.containerPort)
			fmt.Fprintf(&forwards, "for i in $(seq 1 60); do socat -T1 /dev/null TCP:127.0.0.1:%d 2>/dev/null && break; sleep 2; done\n", p.hostPort)
		}
	}
	return c, forwards.String()
}

// allServiceAliases lists every alias in services, sorted so repeated calls
// generate the same script text and Dagger's build cache can hit.
func allServiceAliases(services map[string]composeService) []string {
	aliases := make([]string, 0, len(services))
	for alias := range services {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases
}

// withRepo matches the shared "env:" block unit-integration-tests.yml sets
// for both test-core and test-contrib.
func withRepo(c *dagger.Container, source *dagger.Directory, buildTags, resultsDir string) *dagger.Container {
	return c.
		WithMountedDirectory("/workspace", source, dagger.ContainerWithMountedDirectoryOpts{Owner: "dd-trace-go"}).
		WithWorkdir("/workspace").
		WithEnvVariable("INTEGRATION", "true").
		WithEnvVariable("GOTOOLCHAIN", "local").
		WithEnvVariable("GODEBUG", "x509negativeserial=1").
		WithEnvVariable("BUILD_TAGS", buildTags).
		WithEnvVariable("TEST_RESULTS", resultsDir)
}

// TestCore runs the core test suite, matching scripts/ci_test_core.sh and the
// "Test Core" step of unit-integration-tests.yml. It binds only the Datadog
// Agent service, since that script needs no other service.
func (m *Ci) TestCore(ctx context.Context, source *dagger.Directory, goVersion string,
	// +optional
	buildTags string,
	// +optional
	platform string,
) (*dagger.Directory, error) {
	resolvedPlatform := resolvePlatform(platform)
	base, err := m.Base(source, goVersion, string(resolvedPlatform))
	if err != nil {
		return nil, err
	}
	services, err := loadComposeServices(ctx, source)
	if err != nil {
		return nil, err
	}
	const resultsDir = "/home/dd-trace-go/results"
	c, forwards := bindServices(services, base, resolvedPlatform, "datadog-agent")
	c = withRepo(c, source, buildTags, resultsDir).
		WithEnvVariable("DD_APPSEC_WAF_TIMEOUT", "1h")

	script := forwards +
		"mkdir -p \"$TEST_RESULTS\"\n" +
		"./scripts/ci_test_core.sh > \"$TEST_RESULTS/script.log\" 2>&1\n" +
		"status=$?\n" +
		"cp coverage.txt coverage-noshuffle.txt \"$TEST_RESULTS\"/ 2>/dev/null || true\n" +
		"cp internal/exectracetest/coverage.txt \"$TEST_RESULTS/exectracetest-coverage.txt\" 2>/dev/null || true\n" +
		"echo \"$status\" > \"$TEST_RESULTS/exit-code\"\n" +
		"exit \"$status\"\n"

	c = c.WithExec([]string{"sh", "-c", script}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	return c.Directory(resultsDir), nil
}

// TestContrib runs the contrib test suite, matching scripts/ci_test_contrib.sh
// and the "Test Contrib" step of unit-integration-tests.yml.
//
// contribs is a space-separated list of contrib directories, for example
// "./contrib/net/http ./contrib/gorilla/mux". Leave it empty to run every
// contrib and instrumentation module.
func (m *Ci) TestContrib(ctx context.Context, source *dagger.Directory, goVersion string,
	// +optional
	buildTags string,
	// +optional
	contribs string,
	// +optional
	platform string,
) (*dagger.Directory, error) {
	resolvedPlatform := resolvePlatform(platform)
	base, err := m.Base(source, goVersion, string(resolvedPlatform))
	if err != nil {
		return nil, err
	}
	services, err := loadComposeServices(ctx, source)
	if err != nil {
		return nil, err
	}
	const resultsDir = "/home/dd-trace-go/results"
	c, forwards := bindServices(services, base, resolvedPlatform, allServiceAliases(services)...)
	c = withRepo(c, source, buildTags, resultsDir)

	runContrib := "./scripts/ci_test_contrib.sh default"
	if contribs != "" {
		runContrib = fmt.Sprintf("./scripts/ci_test_contrib.sh default %q", contribs)
	}

	script := forwards +
		"mkdir -p \"$TEST_RESULTS\"\n" +
		runContrib + " > \"$TEST_RESULTS/script.log\" 2>&1\n" +
		"status=$?\n" +
		"echo \"mode: atomic\" > \"$TEST_RESULTS/coverage-contrib-merged.txt\"\n" +
		"find . \\( -path \"*/contrib/*\" -o -path \"*/instrumentation/*\" \\) -name \"coverage-*.txt\" -print0 " +
		"| xargs -0 grep -h -v \"^mode:\" >> \"$TEST_RESULTS/coverage-contrib-merged.txt\" 2>/dev/null || true\n" +
		"echo \"$status\" > \"$TEST_RESULTS/exit-code\"\n" +
		"exit \"$status\"\n"

	c = c.WithExec([]string{"sh", "-c", script}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	return c.Directory(resultsDir), nil
}
