// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package ibm_sarama

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/require"
)

// TestConstructorsAreWrapped checks that every sarama constructor call is
// wrapped, including the ...FromClient variants and calls whose arguments
// come from a single multi-value call, which cannot be rewritten. It uses a
// sarama mock broker, so it needs no Kafka.
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
