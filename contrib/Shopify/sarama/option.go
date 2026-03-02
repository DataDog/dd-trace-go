// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/Shopify/sarama"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var clusterIDCache sync.Map // normalized bootstrap servers -> cluster ID

const defaultServiceName = "kafka"

type config struct {
	consumerServiceName string
	producerServiceName string
	consumerSpanName    string
	producerSpanName    string
	analyticsRate       float64
	dataStreamsEnabled  bool
	groupID             string
	clusterID           string
}

func defaults(cfg *config) {
	cfg.consumerServiceName = instr.ServiceName(instrumentation.ComponentConsumer, nil)
	cfg.producerServiceName = instr.ServiceName(instrumentation.ComponentProducer, nil)

	cfg.consumerSpanName = instr.OperationName(instrumentation.ComponentConsumer, nil)
	cfg.producerSpanName = instr.OperationName(instrumentation.ComponentProducer, nil)

	cfg.dataStreamsEnabled = instr.DataStreamsEnabled()

	cfg.analyticsRate = instr.AnalyticsRate(false)
}

// Option describes options for the Sarama integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to WrapConsumer, WrapPartitionConsumer, WrapAsyncProducer and WrapSyncProducer.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

// WithService sets the given service name for the intercepted client.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.consumerServiceName = name
		cfg.producerServiceName = name
	}
}

// WithDataStreams enables the Data Streams monitoring product features: https://www.datadoghq.com/product/data-streams-monitoring/
func WithDataStreams() OptionFn {
	return func(cfg *config) {
		cfg.dataStreamsEnabled = true
	}
}

// WithGroupID tags the produced data streams metrics with the given groupID (aka consumer group)
func WithGroupID(groupID string) OptionFn {
	return func(cfg *config) {
		cfg.groupID = groupID
	}
}

// WithBrokers enables automatic detection of the Kafka cluster ID by connecting
// to the given broker addresses and fetching cluster metadata. The result is
// cached by the broker address list so that repeated calls with the same brokers
// do not make additional metadata requests.
func WithBrokers(saramaConfig *sarama.Config, addrs []string) OptionFn {
	return func(cfg *config) {
		if len(addrs) == 0 {
			return
		}
		if clusterID := fetchClusterID(saramaConfig, addrs); clusterID != "" {
			cfg.clusterID = clusterID
		}
	}
}

// normalizeBootstrapServers returns a canonical form of a list of broker
// addresses. It trims whitespace, removes empty entries, and sorts entries
// lexicographically.
func normalizeBootstrapServers(addrs []string) string {
	var parts []string
	for _, s := range addrs {
		s = strings.TrimSpace(s)
		if s != "" {
			parts = append(parts, s)
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

func fetchClusterID(saramaConfig *sarama.Config, addrs []string) string {
	key := normalizeBootstrapServers(addrs)
	if key == "" {
		return ""
	}
	if v, ok := clusterIDCache.Load(key); ok {
		return v.(string)
	}

	broker := sarama.NewBroker(addrs[0])
	if err := broker.Open(saramaConfig); err != nil {
		instr.Logger().Warn("contrib/Shopify/sarama: failed to open broker for cluster ID: %s", err)
		return ""
	}
	defer broker.Close()

	resp, err := broker.GetMetadata(&sarama.MetadataRequest{Version: 4})
	if err != nil {
		instr.Logger().Warn("contrib/Shopify/sarama: failed to fetch Kafka cluster ID: %s", err)
		return ""
	}
	if resp.ClusterID == nil {
		return ""
	}

	clusterIDCache.Store(key, *resp.ClusterID)
	return *resp.ClusterID
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
	return func(cfg *config) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) OptionFn {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}
