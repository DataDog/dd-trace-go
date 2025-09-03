/*
 * Unless explicitly stated otherwise all files in this repository are licensed
 * under the Apache License Version 2.0.
 *
 * This product includes software developed at Datadog (https://www.datadoghq.com/).
 * Copyright 2021 Datadog, Inc.
 */

package metrics

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type (
	// Batcher aggregates metrics with common properties,(metric name, tags, type etc)
	Batcher struct {
		metrics       map[string]Metric
		batchInterval time.Duration
	}
	// BatchKey identifies a batch of metrics
	BatchKey struct {
		metricType MetricType
		name       string
		tags       []string
		host       *string
	}
)

// MakeBatcher creates a new batcher object
func MakeBatcher(batchInterval time.Duration) *Batcher {
	return &Batcher{
		batchInterval: batchInterval,
		metrics:       map[string]Metric{},
	}
}

// AddMetric adds a point to a given metric
func (b *Batcher) AddMetric(metric Metric) {
	sk := b.getStringKey(metric.ToBatchKey())
	if existing, ok := b.metrics[sk]; ok {
		existing.Join(metric)
	} else {
		b.metrics[sk] = metric
	}
}

// ToAPIMetrics converts the current batch of metrics into API metrics
func (b *Batcher) ToAPIMetrics() []APIMetric {

	ar := []APIMetric{}
	interval := b.batchInterval / time.Second

	for _, metric := range b.metrics {
		values := metric.ToAPIMetric(interval)
		ar = append(ar, values...)
	}
	return ar
}

func (b *Batcher) getStringKey(bk BatchKey) string {
	tagKey := getTagKey(bk.tags)

	if bk.host != nil {
		return fmt.Sprintf("(%s)-(%s)-(%s)-(%s)", bk.metricType, bk.name, tagKey, *bk.host)
	}
	return fmt.Sprintf("(%s)-(%s)-(%s)", bk.metricType, bk.name, tagKey)
}

func getTagKey(tags []string) string {
	sortedTags := make([]string, len(tags))
	copy(sortedTags, tags)
	sort.Strings(sortedTags)
	return strings.Join(sortedTags, ":")
}
