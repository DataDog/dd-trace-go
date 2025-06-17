package declarativeconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeclarativeConfigSource(t *testing.T) {
	data := `
file_format: "0.4"
disabled: false
log_level: debug`
	var dc declarativeConfig
	unmarshal([]byte(data), &dc)
	assert.Equal(t, "0.4", dc.fileFormat)
	assert.Equal(t, false, dc.disabled)
	assert.Equal(t, "debug", dc.logLevel)
}
