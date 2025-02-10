// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package os_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	wrapos "gopkg.in/DataDog/dd-trace-go.v1/contrib/os"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/ossec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/addresses"
	lfi "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/ossec"
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
