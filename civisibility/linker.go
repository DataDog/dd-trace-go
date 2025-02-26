package civisibility

// Let's import all the internal package so we can enable the go:linkname directive over the internal packages
import (
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting/coverage"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/net"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/telemetry"
)
