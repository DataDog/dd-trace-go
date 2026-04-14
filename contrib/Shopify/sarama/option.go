// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"
	"math"
	"sync/atomic"

	"github.com/Shopify/sarama"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const defaultServiceName = "kafka"

type config struct {
	consumerServiceName string
	producerServiceName string
	serviceSource       string
	consumerSpanName    string
	producerSpanName    string
	analyticsRate       float64
	dataStreamsEnabled  bool
	groupID             string
	clusterID           atomic.Value // +checkatomic
	brokerAddrs         []string
	saramaConfig        *sarama.Config
}

func (cfg *config) ClusterID() string {
	v, _ := cfg.clusterID.Load().(string)
	return v
}

func (cfg *config) SetClusterID(id string) {
	cfg.clusterID.Store(id)
}

func defaults(cfg *config) {
	cfg.consumerServiceName = instr.ServiceName(instrumentation.ComponentConsumer, nil)
	cfg.producerServiceName = instr.ServiceName(instrumentation.ComponentProducer, nil)
	cfg.serviceSource = string(instrumentation.PackageShopifySarama)

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
		cfg.serviceSource = instrumentation.ServiceSourceWithServiceOption
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

// WithBrokers provides broker addresses for automatic Kafka cluster ID
// detection for Data Streams Monitoring. The cluster ID is fetched
// asynchronously in the background when the producer or consumer is wrapped.
func WithBrokers(addrs []string) OptionFn {
	return func(cfg *config) {
		cfg.brokerAddrs = addrs
	}
}

// startClusterIDFetch launches a goroutine to fetch the cluster ID from one of
// the configured brokers. It returns a stop function that cancels the fetch and
// waits for the goroutine to exit.
func startClusterIDFetch(cfg *config) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		clusterID := fetchClusterID(ctx, cfg.saramaConfig, cfg.brokerAddrs)
		if clusterID == "" {
			return
		}
		cfg.SetClusterID(clusterID)
	}()
	return func() {
		cancel()
		<-done
	}
}

// fetchClusterID connects to the first available broker and fetches the cluster ID.
func fetchClusterID(ctx context.Context, saramaConfig *sarama.Config, addrs []string) string {
	if saramaConfig == nil {
		saramaConfig = sarama.NewConfig()
	}
	for _, addr := range addrs {
		if ctx.Err() != nil {
			return ""
		}
		broker := sarama.NewBroker(addr)
		if err := broker.Open(saramaConfig); err != nil {
			instr.Logger().Debug("contrib/Shopify/sarama: failed to open broker %s for cluster ID: %s", addr, err)
			continue
		}
		resp, err := broker.GetMetadata(&sarama.MetadataRequest{Version: 4})
		_ = broker.Close()
		if err != nil {
			if ctx.Err() != nil {
				return ""
			}
			instr.Logger().Debug("contrib/Shopify/sarama: failed to get metadata from broker %s: %s", addr, err)
			continue
		}
		if resp.ClusterID == nil || *resp.ClusterID == "" {
			continue
		}
		return *resp.ClusterID
	}
	instr.Logger().Warn("contrib/Shopify/sarama: could not fetch Kafka cluster ID from any broker")
	return ""
}
