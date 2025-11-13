package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func TestEnvConfigSource(t *testing.T) {
	envConfigSource := &envConfigSource{}
	t.Setenv("DD_SERVICE", "value")
	assert.Equal(t, "value", envConfigSource.Get("DD_SERVICE"))
	assert.Equal(t, telemetry.OriginEnvVar, envConfigSource.Origin())
}

func TestNormalizedEnvConfigSource(t *testing.T) {
	envConfigSource := &envConfigSource{}
	t.Setenv("DD_SERVICE", "value")
	assert.Equal(t, "value", envConfigSource.Get("service"))
}
