// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ibm_sarama

import (
	"context"
	"errors"
	"testing"

	"github.com/IBM/sarama"
	shopifysarama "github.com/Shopify/sarama"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/ibm_sarama/internal/kafkamock"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// shopifyTopic is the topic of the Shopify/sarama broker in TestCaseBothForks.
const shopifyTopic = "both_forks_shopify_sarama_mock_test"

// TestCaseBothForks calls a producer constructor of each sarama fork in one
// file, as during a migration from Shopify/sarama to IBM/sarama. Each producer
// must get the contrib of its own fork.
type TestCaseBothForks struct {
	mockBroker
	shopifyAddrs []string
}

func (tc *TestCaseBothForks) Setup(ctx context.Context, t *testing.T) {
	tc.mockBroker.Setup(ctx, t)

	b := shopifysarama.NewMockBroker(t, 1)
	t.Cleanup(b.Close)
	b.SetHandlerByMap(map[string]shopifysarama.MockResponse{
		"MetadataRequest": shopifysarama.NewMockMetadataResponse(t).
			SetBroker(b.Addr(), b.BrokerID()).
			SetLeader(shopifyTopic, 0, b.BrokerID()),
		// This sarama version does not answer with the request's version.
		"ProduceRequest": shopifysarama.NewMockProduceResponse(t).SetVersion(3),
	})
	tc.shopifyAddrs = []string{b.Addr()}
}

func (tc *TestCaseBothForks) Run(_ context.Context, t *testing.T) {
	p, err := sarama.NewSyncProducer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.SendSync(p))

	cfg := shopifysarama.NewConfig()
	cfg.Version = shopifysarama.V0_11_0_0
	cfg.Producer.Return.Successes = true
	cfg.Producer.Retry.Backoff = 0
	sp, err := shopifysarama.NewSyncProducer(tc.shopifyAddrs, cfg)
	require.NoError(t, err)
	_, _, err = sp.SendMessage(&shopifysarama.ProducerMessage{
		Topic: shopifyTopic,
		Value: shopifysarama.StringEncoder(kafkamock.Message),
	})
	require.NoError(t, errors.Join(err, sp.Close()))
}

func (*TestCaseBothForks) ExpectedTraces() trace.Traces {
	return trace.Traces{
		produceSpan(),
		{
			Tags: map[string]any{
				"name":     "kafka.produce",
				"type":     "queue",
				"service":  "kafka",
				"resource": "Produce Topic " + shopifyTopic,
			},
			Meta: map[string]string{
				"span.kind": "producer",
				"component": "Shopify/sarama",
			},
		},
	}
}
func (*TestCaseBothForks) ExpectedSpanCount() int { return 2 }
