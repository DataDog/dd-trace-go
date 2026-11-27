// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package ibm_sarama

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

// TestConstructorsAreWrapped checks that every sarama constructor call is
// wrapped, including the ...FromClient variants and calls whose arguments
// come from a single multi-value call. It uses a sarama mock broker, so it
// needs no Kafka.
func TestConstructorsAreWrapped(t *testing.T) {
	b := sarama.NewMockBroker(t, 1)
	defer b.Close()
	b.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(b.Addr(), b.BrokerID()).
			SetController(b.BrokerID()),
	})
	cfg := sarama.NewConfig()
	cfg.Producer.Return.Successes = true
	brokersAndConfig := func() ([]string, *sarama.Config) { return []string{b.Addr()}, cfg }
	client, err := sarama.NewClient([]string{b.Addr()}, cfg)
	require.NoError(t, err)
	defer client.Close()

	requireWrapped := func(name string, v any) {
		t.Helper()
		pkg := reflect.TypeOf(v).Elem().PkgPath()
		require.True(t, strings.HasPrefix(pkg, "github.com/DataDog/dd-trace-go/contrib/IBM/sarama"), "%s returned a %s value", name, pkg)
	}
	closers := make([]interface{ Close() error }, 0, 7)
	defer func() {
		for _, c := range slices.Backward(closers) {
			_ = c.Close()
		}
	}()

	c, err := sarama.NewConsumer([]string{b.Addr()}, cfg)
	require.NoError(t, err)
	closers = append(closers, c)
	requireWrapped("NewConsumer", c)
	c, err = sarama.NewConsumer(brokersAndConfig())
	require.NoError(t, err)
	closers = append(closers, c)
	requireWrapped("NewConsumer with a multi-value argument", c)
	c, err = sarama.NewConsumerFromClient(client)
	require.NoError(t, err)
	closers = append(closers, c)
	requireWrapped("NewConsumerFromClient", c)

	sp, err := sarama.NewSyncProducer(brokersAndConfig())
	require.NoError(t, err)
	closers = append(closers, sp)
	requireWrapped("NewSyncProducer with a multi-value argument", sp)
	sp, err = sarama.NewSyncProducerFromClient(client)
	require.NoError(t, err)
	closers = append(closers, sp)
	requireWrapped("NewSyncProducerFromClient", sp)

	ap, err := sarama.NewAsyncProducer(brokersAndConfig())
	require.NoError(t, err)
	closers = append(closers, ap)
	requireWrapped("NewAsyncProducer with a multi-value argument", ap)
	ap, err = sarama.NewAsyncProducerFromClient(client)
	require.NoError(t, err)
	closers = append(closers, ap)
	requireWrapped("NewAsyncProducerFromClient", ap)
}

// TestMultiValueAsyncProducer checks that an async producer created from a
// single multi-value call gets the configuration and the broker addresses of
// that call: its produce spans carry the partition, the offset and the
// bootstrap servers.
func TestMultiValueAsyncProducer(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	b := sarama.NewMockBroker(t, 1)
	defer b.Close()
	b.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(b.Addr(), b.BrokerID()).
			SetLeader("topic", 0, b.BrokerID()).
			SetController(b.BrokerID()),
		"ProduceRequest": sarama.NewMockProduceResponse(t).
			SetVersion(3).
			SetError("topic", 0, sarama.ErrNoError),
	})
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_11_0_0
	cfg.Producer.Return.Successes = true
	brokersAndConfig := func() ([]string, *sarama.Config) { return []string{b.Addr()}, cfg }

	ap, err := sarama.NewAsyncProducer(brokersAndConfig())
	require.NoError(t, err)
	ap.Input() <- &sarama.ProducerMessage{Topic: "topic", Value: sarama.StringEncoder("value")}
	select {
	case <-ap.Successes():
	case err := <-ap.Errors():
		t.Fatalf("produce failed: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the produce result")
	}
	require.NoError(t, ap.Close())

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	s := spans[0]
	assert.Equal(t, b.Addr(), s.Tag(ext.KafkaBootstrapServers))
	assert.EqualValues(t, 0, s.Tag(ext.MessagingKafkaPartition))
	assert.NotNil(t, s.Tag("offset"), "the offset is only known with the configuration of the call")
}
