// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package shopify_sarama

import (
	"context"
	"fmt"
	"testing"

	"github.com/Shopify/sarama"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/shopify_sarama/internal/kafkamock"
)

// The broker and clients are created during package initialization, before
// the tracer starts.
var (
	pkgBroker                   = kafkamock.Start(initReporter{})
	pkgProducer, pkgProducerErr = sarama.NewSyncProducer([]string{pkgBroker.Addr()}, kafkamock.Config())
	pkgConsumer, pkgConsumerErr = sarama.NewConsumer([]string{pkgBroker.Addr()}, kafkamock.Config())
)

// initReporter reports the broker's failures where no test is running yet.
type initReporter struct{}

func (initReporter) Error(args ...any)                 { panic(fmt.Sprint(args...)) }
func (initReporter) Errorf(format string, args ...any) { panic(fmt.Sprintf(format, args...)) }
func (r initReporter) Fatal(args ...any)               { r.Error(args...) }
func (r initReporter) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
}
func (initReporter) Helper() {}

// TestCasePackageVar checks that a producer and a consumer created in
// package-level variables are traced once.
type TestCasePackageVar struct{}

func (*TestCasePackageVar) Setup(_ context.Context, t *testing.T) {
	t.Cleanup(pkgBroker.Close)
	require.NoError(t, pkgProducerErr)
	require.NoError(t, pkgConsumerErr)
}

func (*TestCasePackageVar) Run(_ context.Context, t *testing.T) {
	require.NoError(t, kafkamock.SendSync(pkgProducer))
	require.NoError(t, kafkamock.Consume(pkgConsumer))
}

func (*TestCasePackageVar) ExpectedTraces() trace.Traces {
	return trace.Traces{produceSpan(), consumeSpan()}
}
func (*TestCasePackageVar) ExpectedSpanCount() int { return 2 }
