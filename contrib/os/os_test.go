// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package os_test

import (
	"context"
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	wrapos "github.com/DataDog/dd-trace-go/v2/contrib/os"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/ossec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	lfi "github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/ossec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenFile(t *testing.T) {
	ctx := context.Background()
	rootOp := dyngo.NewRootOperation()
	feature, err := lfi.NewOSSecFeature(
		&config.Config{
			RASP:               true,
			SupportedAddresses: map[string]struct{}{addresses.ServerIOFSFileAddr: {}},
		},
		rootOp,
	)
	require.NoError(t, err)
	defer feature.Stop()

	ctx = dyngo.RegisterOperation(ctx, rootOp)
	dyngo.On(rootOp, func(op *ossec.OpenOperation, args ossec.OpenOperationArgs) {
		// We shall block this request!
		dyngo.EmitData(op, &events.BlockingSecurityEvent{})

		assert.Equal(t, "/etc/passwd", args.Path)
		assert.Equal(t, os.O_RDONLY, args.Flags)
		assert.Equal(t, os.FileMode(0), args.Perms)
	})

	file, err := wrapos.OpenFile(ctx, "/etc/passwd", os.O_RDONLY, 0)
	require.ErrorContains(t, err, "blocked")
	require.Nil(t, file)
}
