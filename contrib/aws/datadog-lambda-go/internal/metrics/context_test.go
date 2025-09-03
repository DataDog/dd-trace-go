/*
 * Unless explicitly stated otherwise all files in this repository are licensed
 * under the Apache License Version 2.0.
 *
 * This product includes software developed at Datadog (https://www.datadoghq.com/).
 * Copyright 2021 Datadog, Inc.
 */

package metrics

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/contrib/aws/datadog-lambda-go/internal/extension"
	"github.com/stretchr/testify/assert"
)

func TestGetProcessorEmptyContext(t *testing.T) {
	ctx := context.Background()
	result := GetListener(ctx)
	assert.Nil(t, result)
}

func TestGetProcessorSuccess(t *testing.T) {
	lst := MakeListener(Config{}, &extension.ExtensionManager{})
	ctx := AddListener(context.Background(), &lst)
	result := GetListener(ctx)
	assert.NotNil(t, result)
}
