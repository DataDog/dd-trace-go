/*
 * Unless explicitly stated otherwise all files in this repository are licensed
 * under the Apache License Version 2.0.
 *
 * This product includes software developed at Datadog (https://www.datadoghq.com/).
 * Copyright 2021 Datadog, Inc.
 */

package metrics

import (
	"time"
)

type (
	// Metric represents a metric that can have any kind of
	Metric interface {
		AddPoint(timestamp time.Time, value float64)
		ToAPIMetric(interval time.Duration) []APIMetric
		ToBatchKey() BatchKey
		Join(metric Metric)
	}

	// APIMetric is a metric that can be marshalled to send to the metrics API
	APIMetric struct {
		Name       string        `json:"metric"`
		Host       *string       `json:"host,omitempty"`
		Tags       []string      `json:"tags,omitempty"`
		MetricType MetricType    `json:"type"`
		Interval   *float64      `json:"interval,omitempty"`
		Points     []interface{} `json:"points"`
	}

	// MetricValue represents a datapoint for a metric
	MetricValue struct {
		Value     float64
		Timestamp time.Time
	}

	// Distribution is a type of metric that is aggregated over multiple hosts
	Distribution struct {
		Name   string
		Tags   []string
		Host   *string
		Values []MetricValue
	}
)

// AddPoint adds a point to the distribution metric
func (d *Distribution) AddPoint(timestamp time.Time, value float64) {
	d.Values = append(d.Values, MetricValue{Timestamp: timestamp, Value: value})
}

// ToBatchKey returns a key that can be used to batch the metric
func (d *Distribution) ToBatchKey() BatchKey {
	return BatchKey{
		name:       d.Name,
		host:       d.Host,
		tags:       d.Tags,
		metricType: DistributionType,
	}
}

// Join creates a union between two metric sets
func (d *Distribution) Join(metric Metric) {
	otherDist, ok := metric.(*Distribution)
	if !ok {
		return
	}
	for _, val := range otherDist.Values {
		d.AddPoint(val.Timestamp, val.Value)
	}

}

// ToAPIMetric converts a distribution into an API ready format.
func (d *Distribution) ToAPIMetric(interval time.Duration) []APIMetric {
	points := make([]interface{}, len(d.Values))

	for i, val := range d.Values {
		currentTime := float64(val.Timestamp.Unix())

		points[i] = []interface{}{currentTime, []interface{}{val.Value}}
	}

	return []APIMetric{
		APIMetric{
			Name:       d.Name,
			Host:       d.Host,
			Tags:       d.Tags,
			MetricType: DistributionType,
			Points:     points,
			Interval:   nil,
		},
	}
}
