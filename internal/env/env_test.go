package env

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerifySupportedConfiguration(t *testing.T) {
	// Known configuration
	t.Setenv("DD_TRACE_BAR", "VALUE")
	res, ok := LookupEnv("DD_TRACE_BAR")
	require.True(t, ok)
	require.Equal(t, "VALUE", res)

	// Unknown configuration with no adding to the supported configurations
	// file.
	t.Setenv("DD_CONFIG_INVERSION_UNKNOWN", "VALUE")
	res, ok = LookupEnv("DD_CONFIG_INVERSION_UNKNOWN")
	require.False(t, ok)
	require.Empty(t, res)
}
