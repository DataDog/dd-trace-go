package telemetrytest

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/mux"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

func TestIntegration(t *testing.T) {
	mux.NewRouter()
	integrations := telemetry.Integrations()
	assert.Len(t, integrations, 1)
	assert.Equal(t, integrations[0].Name, "gorilla/mux")
	assert.True(t, integrations[0].Enabled)
}

func TestIntegrations(t *testing.T) {
	// list all packages in the contrib
	cmd := "go list -json ../.././... | grep -v '/internal'"
	out, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		t.Fatalf(err.Error())
	}
	numIntegrations := len(strings.Split(string(out), "\n"))
}
