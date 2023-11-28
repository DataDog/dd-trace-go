package mux

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegrationInfo verifies that an integration leveraging instrumentation telemetry
// sends the correct data to the telemetry client.
func TestIntegrationInfo(t *testing.T) {
	// mux.NewRouter() uses the net/http and gorilla/mux integration
	NewRouter()
	integrations := telemetry.Integrations()
	require.Len(t, integrations, 2)
	assert.Equal(t, integrations[0].Name, "net/http")
	assert.True(t, integrations[0].Enabled)
	assert.Equal(t, integrations[1].Name, "gorilla/mux")
	assert.True(t, integrations[1].Enabled)
}
