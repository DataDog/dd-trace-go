// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestStartRejectsInvalidInheritedPipe(t *testing.T) {
	parent := strconv.Itoa(os.Getppid())
	t.Setenv("LISTEN_PID", parent)
	for _, value := range []string{"", "not-a-number", "-1", "0", "1", "2", "999999999"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(supervisedFDEnv, parent+":"+value)
			err := start(func(*config) { t.Fatal("child applied options") })
			if err == nil {
				t.Fatal("Start accepted an invalid crash descriptor")
			}
			if _, ok := os.LookupEnv(supervisedFDEnv); ok {
				t.Fatal("Start retained the invalid marker")
			}
			// The socket handoff must not depend on the pipe validation.
			if got, want := os.Getenv("LISTEN_PID"), strconv.Itoa(os.Getpid()); got != want {
				t.Fatalf("invalid crash pipe left LISTEN_PID at %q, want %q", got, want)
			}
		})
	}
}

func TestStartClosesInvalidInheritedPipe(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-pipe")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	t.Setenv(supervisedFDEnv, strconv.Itoa(os.Getppid())+":"+strconv.FormatUint(uint64(f.Fd()), 10))
	if err := start(); err == nil {
		t.Fatal("Start accepted a regular file as the crash pipe")
	}
	if _, err := f.Stat(); err == nil {
		t.Fatal("Start left the invalid inherited descriptor open")
	}
}

func TestParseSupervisedMarker(t *testing.T) {
	parent := strconv.Itoa(os.Getppid())
	foreign := strconv.Itoa(os.Getppid() + 1)
	for _, test := range []struct {
		name   string
		marker string
		fd     string
		ok     bool
	}{
		{"bound to the parent", parent + ":7", "7", true},
		{"bad fd still bound", parent + ":x", "x", true},
		{"empty fd still bound", parent + ":", "", true},
		{"bare number", "7", "", false},
		{"foreign pid", foreign + ":7", "", false},
		{"empty", "", "", false},
		{"pid prefix only", parent + "0:7", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fd, ok := parseSupervisedMarker(test.marker)
			if fd != test.fd || ok != test.ok {
				t.Fatalf("parseSupervisedMarker(%q) = %q, %v, want %q, %v", test.marker, fd, ok, test.fd, test.ok)
			}
		})
	}
}

func TestResolveServiceFallsBackToExecutableName(t *testing.T) {
	// Neither globalconfig.SetServiceName (the tracer's job, not this
	// package's) nor DD_SERVICE has run/been set in this test binary, so this
	// exercises the final fallback.
	t.Setenv("DD_SERVICE", "")

	got := resolveService(nil)
	want := filepath.Base(os.Args[0])
	if got != want {
		t.Errorf("resolveService(nil) = %q, want %q (filepath.Base(os.Args[0]), matching tracer.Start's own default)", got, want)
	}
}

// TestResolveServiceUsesDDTags proves service falls back to a DD_TAGS-embedded
// value before the executable-name default, matching the tracer's own
// DD_TAGS fallback (ddtrace/tracer/option.go) so a crash report attributes to
// the same service as that process's traces when only DD_TAGS sets it.
func TestResolveServiceUsesDDTags(t *testing.T) {
	t.Setenv("DD_SERVICE", "")

	got := resolveService(map[string]string{"service": "checkout-api"})
	if got != "checkout-api" {
		t.Errorf(`resolveService(DD_TAGS service) = %q, want "checkout-api"`, got)
	}
}

// TestResolveTagPrefersExplicitEnvOverDDTags proves an explicit DD_ENV/
// DD_VERSION wins over a same-named DD_TAGS entry, matching the tracer's own
// precedence rather than DD_TAGS silently overriding an explicit setting.
func TestResolveTagPrefersExplicitEnvOverDDTags(t *testing.T) {
	t.Setenv("DD_ENV", "prod")

	got := resolveTag("DD_ENV", "env", map[string]string{"env": "staging"})
	if got != "prod" {
		t.Errorf(`resolveTag("DD_ENV", ...) = %q, want "prod" (explicit env wins over DD_TAGS)`, got)
	}
}

// TestResolveTagFallsBackToDDTags proves env/version resolve from a DD_TAGS
// entry when the dedicated env var is unset, the gap this test guards:
// previously DD_ENV/DD_VERSION read only their own env var and never
// consulted DD_TAGS at all, unlike the tracer.
func TestResolveTagFallsBackToDDTags(t *testing.T) {
	t.Setenv("DD_VERSION", "")

	got := resolveTag("DD_VERSION", "version", map[string]string{"version": "1.2.3"})
	if got != "1.2.3" {
		t.Errorf(`resolveTag("DD_VERSION", ...) = %q, want "1.2.3" (DD_TAGS fallback)`, got)
	}
}

// TestDefaultConfigParsesDDTags proves defaultConfig itself wires DD_TAGS
// through to service/env/version end to end, not just the resolveTag helper
// in isolation.
func TestDefaultConfigParsesDDTags(t *testing.T) {
	t.Setenv("DD_SERVICE", "")
	t.Setenv("DD_ENV", "")
	t.Setenv("DD_VERSION", "")
	t.Setenv("DD_TAGS", "service:checkout-api,env:prod,version:1.2.3")

	cfg := defaultConfig()
	if cfg.service != "checkout-api" {
		t.Errorf("defaultConfig().service = %q, want %q", cfg.service, "checkout-api")
	}
	if cfg.env != "prod" {
		t.Errorf("defaultConfig().env = %q, want %q", cfg.env, "prod")
	}
	if cfg.version != "1.2.3" {
		t.Errorf("defaultConfig().version = %q, want %q", cfg.version, "1.2.3")
	}
}
