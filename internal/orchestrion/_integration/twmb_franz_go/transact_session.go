// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package twmb_franz_go

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// Only the otelc rules cover kgo.NewGroupTransactSession.
const skipGroupTransactSession = "otelc-only coverage: orchestrion does not trace a client created by kgo.NewGroupTransactSession"

// TestCaseGroupTransactSession checks that a client created by
// kgo.NewGroupTransactSession is traced.
type TestCaseGroupTransactSession struct{ TestCase }

func (tc *TestCaseGroupTransactSession) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip(skipGroupTransactSession)
	}
	tc.setup(t, "twmb_franz_go_txn_topic", "twmb_franz_go_txn_group")
}

func (tc *TestCaseGroupTransactSession) Run(ctx context.Context, t *testing.T) {
	sess, err := kgo.NewGroupTransactSession(
		kgo.SeedBrokers(tc.addr),
		kgo.TransactionalID("twmb_franz_go_txn"),
		kgo.ConsumerGroup(tc.group+"_session"),
		kgo.ConsumeTopics(tc.topic),
	)
	require.NoError(t, err)
	consumer, err := kgo.NewClient(append(tc.consumerOpts(), kgo.FetchIsolationLevel(kgo.ReadCommitted()))...)
	require.NoError(t, err)

	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	require.NoError(t, sess.Begin())
	record := &kgo.Record{Topic: tc.topic, Key: []byte("key1"), Value: []byte("Hello World!")}
	err = sess.ProduceSync(ctx, record).FirstErr()
	endCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	committed, endErr := sess.End(endCtx, kgo.TryCommit)
	sess.Close()
	span.Finish()
	require.NoError(t, err)
	require.NoError(t, endErr)
	require.True(t, committed)

	tc.poll(t, consumer)
}
