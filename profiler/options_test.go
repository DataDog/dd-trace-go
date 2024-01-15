// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testAPIKey is an example API key for validation purposes
const testAPIKey = "12345678901234567890123456789012"

func TestOptions(t *testing.T) {
	t.Run("APIKeyChecks", func(t *testing.T) {
		var apikeytests = []struct {
			in  string
			out bool
		}{
			{"", false}, // Fail, empty string
			{"1234567890123456789012345678901", false},   // Fail, too short
			{"123456789012345678901234567890123", false}, // Fail, too long
			{"12345678901234567890123456789012", true},   // Pass, numeric only
			{"abcdefabcdabcdefabcdefabcdefabcd", true},   // Pass, alpha only
			{"abcdefabcdabcdef7890abcdef789012", true},   // Pass, alphanumeric
			{"abcdefabcdabcdef7890Abcdef789012", false},  // Fail, contains an uppercase
			{"abcdefabcdabcdef7890@bcdef789012", false},  // Fail, contains an ASCII symbol
			{"abcdefabcdabcdef7890ábcdef789012", false},  // Fail, lowercase extended ASCII
			{"abcdefabcdabcdef7890ábcdef78901", false},   // Fail, lowercase extended ASCII, conservative
		}

		for i, tt := range apikeytests {
			assert.Equal(t, tt.out, isAPIKeyValid(tt.in), strconv.Itoa(i)+" : "+tt.in)
		}
	})

	t.Run("WithAgentAddr", func(t *testing.T) {
		var cfg config
		WithAgentAddr("test:123")(&cfg)
		expectedURL := "http://test:123/profiling/v1/input"
		assert.Equal(t, expectedURL, cfg.agentURL)
	})

	t.Run("WithAgentAddr/override", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "bad_host")
		t.Setenv("DD_TRACE_AGENT_PORT", "bad_port")
		var cfg config
		WithAgentAddr("test:123")(&cfg)
		expectedURL := "http://test:123/profiling/v1/input"
		assert.Equal(t, expectedURL, cfg.agentURL)
	})

	t.Run("AgentURL", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		expectedURL := "https://custom:1234/profiling/v1/input"
		assert.Equal(t, expectedURL, cfg.agentURL)
	})

	t.Run("AgentURL/override-env", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "testhost")
		t.Setenv("DD_TRACE_AGENT_PORT", "3333")
		t.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		expectedURL := "https://custom:1234/profiling/v1/input"
		assert.Equal(t, expectedURL, cfg.agentURL)
	})

	t.Run("AgentURL/code-override", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		WithAgentAddr("test:1234")(cfg)
		expectedURL := "http://test:1234/profiling/v1/input"
		assert.Equal(t, expectedURL, cfg.agentURL)
	})

	t.Run("WithUploadTimeout", func(t *testing.T) {
		var cfg config
		WithUploadTimeout(5 * time.Second)(&cfg)
		assert.Equal(t, 5*time.Second, cfg.uploadTimeout)
	})

	t.Run("WithAPIKey", func(t *testing.T) {
		var cfg config
		WithAPIKey(testAPIKey)(&cfg)
		assert.Equal(t, testAPIKey, cfg.apiKey)
		assert.Equal(t, cfg.apiURL, cfg.targetURL)
	})

	t.Run("WithAPIKey/override", func(t *testing.T) {
		t.Setenv("DD_API_KEY", "apikey")
		var testAPIKey = "12345678901234567890123456789012"
		var cfg config
		WithAPIKey(testAPIKey)(&cfg)
		assert.Equal(t, testAPIKey, cfg.apiKey)
	})

	t.Run("WithURL", func(t *testing.T) {
		var cfg config
		WithURL("my-url")(&cfg)
		assert.Equal(t, "my-url", cfg.apiURL)
	})

	t.Run("WithPeriod", func(t *testing.T) {
		var cfg config
		WithPeriod(2 * time.Second)(&cfg)
		assert.Equal(t, 2*time.Second, cfg.period)
	})

	t.Run("CPUDuration", func(t *testing.T) {
		var cfg config
		CPUDuration(3 * time.Second)(&cfg)
		assert.Equal(t, 3*time.Second, cfg.cpuDuration)
	})

	t.Run("CPUProfileRate", func(t *testing.T) {
		var cfg config
		CPUProfileRate(1000)(&cfg)
		assert.Equal(t, 1000, cfg.cpuProfileRate)
	})

	t.Run("MutexProfileFraction", func(t *testing.T) {
		var cfg config
		MutexProfileFraction(1)(&cfg)
		assert.Equal(t, 1, cfg.mutexFraction)
		assert.Contains(t, cfg.types, MutexProfile)
	})

	t.Run("BlockProfileRate", func(t *testing.T) {
		var cfg config
		BlockProfileRate(1)(&cfg)
		assert.Equal(t, 1, cfg.blockRate)
		assert.Contains(t, cfg.types, BlockProfile)
	})

	t.Run("WithProfileTypes", func(t *testing.T) {
		var cfg config
		WithProfileTypes(HeapProfile)(&cfg)
		_, ok := cfg.types[HeapProfile]
		assert.True(t, ok)
		assert.Len(t, cfg.types, 2)
	})

	t.Run("WithService", func(t *testing.T) {
		var cfg config
		WithService("serviceName")(&cfg)
		assert.Equal(t, "serviceName", cfg.service)
	})

	t.Run("WithService/override", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "envService")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		WithService("serviceName")(cfg)
		assert.Equal(t, "serviceName", cfg.service)
	})

	t.Run("WithSite", func(t *testing.T) {
		var cfg config
		WithSite("datadog.eu")(&cfg)
		assert.Equal(t, "https://intake.profile.datadog.eu/v1/input", cfg.apiURL)
	})

	t.Run("WithSite/override", func(t *testing.T) {
		t.Setenv("DD_SITE", "wrong.site")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		WithSite("datadog.eu")(cfg)
		assert.Equal(t, "https://intake.profile.datadog.eu/v1/input", cfg.apiURL)
	})

	t.Run("WithEnv", func(t *testing.T) {
		var cfg config
		WithEnv("envName")(&cfg)
		assert.Equal(t, "envName", cfg.env)
	})

	t.Run("WithEnv/override", func(t *testing.T) {
		t.Setenv("DD_ENV", "envEnv")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		WithEnv("envName")(cfg)
		assert.Equal(t, "envName", cfg.env)
	})

	t.Run("WithVersion", func(t *testing.T) {
		var cfg config
		WithVersion("1.2.3")(&cfg)
		assert.Equal(t, cfg.version, "1.2.3")
	})

	t.Run("WithVersion/override", func(t *testing.T) {
		t.Setenv("DD_VERSION", "envVersion")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		WithVersion("1.2.3")(cfg)
		assert.Equal(t, cfg.version, "1.2.3")
	})

	t.Run("WithTags", func(t *testing.T) {
		var cfg config
		WithTags("a:1", "b:2", "c:3")(&cfg)
		tags := cfg.tags.Slice()
		assert.Contains(t, tags, "a:1")
		assert.Contains(t, tags, "b:2")
		assert.Contains(t, tags, "c:3")
	})

	t.Run("WithTags/override", func(t *testing.T) {
		t.Setenv("DD_TAGS", "env1:tag1,env2:tag2")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		WithTags("a:1", "b:2", "c:3")(cfg)
		tags := cfg.tags.Slice()
		assert.Contains(t, tags, "a:1")
		assert.Contains(t, tags, "b:2")
		assert.Contains(t, tags, "c:3")
		assert.Contains(t, tags, "env1:tag1")
		assert.Contains(t, tags, "env2:tag2")
	})

	t.Run("WithDeltaProfiles", func(t *testing.T) {
		var cfg config
		WithDeltaProfiles(true)(&cfg)
		assert.Equal(t, true, cfg.deltaProfiles)
		WithDeltaProfiles(false)(&cfg)
		assert.Equal(t, false, cfg.deltaProfiles)
	})

	t.Run("WithHostname", func(t *testing.T) {
		var cfg config
		WithHostname("example")(&cfg)
		assert.Equal(t, "example", cfg.hostname)
	})
}

func TestEnvVars(t *testing.T) {
	t.Run("DD_AGENT_HOST", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "agent_host_1")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		assert.Equal(t, "http://agent_host_1:8126/profiling/v1/input", cfg.agentURL)
	})

	t.Run("DD_TRACE_AGENT_PORT", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_PORT", "6218")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:6218/profiling/v1/input", cfg.agentURL)
	})

	t.Run("DD_PROFILING_UPLOAD_TIMEOUT", func(t *testing.T) {
		t.Setenv("DD_PROFILING_UPLOAD_TIMEOUT", "3s")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		assert.Equal(t, 3*time.Second, cfg.uploadTimeout)
	})

	t.Run("DD_AGENT_HOST+DD_TRACE_AGENT_PORT", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "agent_host_1")
		t.Setenv("DD_TRACE_AGENT_PORT", "6218")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		assert.Equal(t, "http://agent_host_1:6218/profiling/v1/input", cfg.agentURL)
	})

	t.Run("DD_API_KEY", func(t *testing.T) {
		t.Setenv("DD_API_KEY", testAPIKey)
		cfg, err := defaultConfig()
		require.NoError(t, err)
		assert.Equal(t, testAPIKey, cfg.apiKey)
	})

	t.Run("DD_SITE", func(t *testing.T) {
		t.Setenv("DD_SITE", "datadog.eu")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		assert.Equal(t, "https://intake.profile.datadog.eu/v1/input", cfg.apiURL)
	})

	t.Run("DD_ENV", func(t *testing.T) {
		t.Setenv("DD_ENV", "someEnv")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		assert.Equal(t, "someEnv", cfg.env)
	})

	t.Run("DD_SERVICE", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "someService")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		assert.Equal(t, "someService", cfg.service)
	})

	t.Run("DD_VERSION", func(t *testing.T) {
		t.Setenv("DD_VERSION", "1.2.3")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		assert.Equal(t, cfg.version, "1.2.3")
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		t.Setenv("DD_TAGS", "a:1,b:2,c:3")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		tags := cfg.tags.Slice()
		assert.Contains(t, tags, "a:1")
		assert.Contains(t, tags, "b:2")
		assert.Contains(t, tags, "c:3")
	})

	t.Run("DD_TAGS_SPACES", func(t *testing.T) {
		t.Setenv("DD_TAGS", "a:1 b:2 c")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		tags := cfg.tags.Slice()
		assert.Contains(t, tags, "a:1")
		assert.Contains(t, tags, "b:2")
		assert.Contains(t, tags, "c")
	})

	t.Run("DD_PROFILING_DELTA", func(t *testing.T) {
		t.Setenv("DD_PROFILING_DELTA", "false")
		cfg, err := defaultConfig()
		require.NoError(t, err)
		assert.Equal(t, cfg.deltaProfiles, false)
	})
}

func TestDefaultConfig(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		defaultAgentURL := "http://" + net.JoinHostPort(defaultAgentHost, defaultAgentPort) + "/profiling/v1/input"
		cfg, err := defaultConfig()
		require.NoError(t, err)
		assert := assert.New(t)
		assert.Equal(defaultAPIURL, cfg.apiURL)
		assert.Equal(defaultAgentURL, cfg.agentURL)
		assert.Equal("", cfg.env)
		assert.Equal(filepath.Base(os.Args[0]), cfg.service)
		assert.Equal(len(defaultProfileTypes), len(cfg.types))
		for _, pt := range defaultProfileTypes {
			_, ok := cfg.types[pt]
			assert.True(ok)
		}
		_, ok := cfg.statsd.(*statsd.NoOpClient)
		assert.True(ok)
		assert.Equal(DefaultPeriod, cfg.period)
		assert.Equal(DefaultDuration, cfg.cpuDuration)
		assert.Equal(0, cfg.cpuProfileRate)
		assert.Equal(DefaultMutexFraction, cfg.mutexFraction)
		assert.Equal(DefaultBlockRate, cfg.blockRate)
		assert.Contains(cfg.tags.Slice(), "runtime-id:"+globalconfig.RuntimeID())
		assert.Equal(true, cfg.deltaProfiles)
	})
}

func TestAddProfileType(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		cfg, err := defaultConfig()
		require.NoError(t, err)
		_, ok := cfg.types[MutexProfile]
		assert.False(ok)
		n := len(cfg.types)
		cfg.addProfileType(MutexProfile)
		assert.Len(cfg.types, n+1)
		_, ok = cfg.types[MutexProfile]
		assert.True(ok)
	})

	t.Run("nil", func(t *testing.T) {
		var cfg config
		assert := assert.New(t)
		assert.Nil(cfg.types)
		cfg.addProfileType(MutexProfile)
		assert.Len(cfg.types, 1)
		_, ok := cfg.types[MutexProfile]
		assert.True(ok)
	})
}

func TestWith_outputDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Use env to enable this like a user would.
	t.Setenv("DD_PROFILING_OUTPUT_DIR", tmpDir)

	p, err := unstartedProfiler()
	require.NoError(t, err)
	bat := batch{
		end: time.Now(),
		profiles: []*profile{
			{name: "foo.pprof", data: []byte("foo")},
			{name: "bar.pprof", data: []byte("bar")},
		},
	}
	require.NoError(t, p.outputDir(bat))
	files, err := filepath.Glob(filepath.Join(tmpDir, "*", "*.pprof"))
	require.NoError(t, err)

	fileData := map[string]string{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		fileData[filepath.Base(file)] = string(data)
	}
	want := map[string]string{"foo.pprof": "foo", "bar.pprof": "bar"}
	require.Equal(t, want, fileData)
}
