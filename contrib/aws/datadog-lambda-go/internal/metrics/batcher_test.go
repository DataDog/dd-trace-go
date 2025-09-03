/*
 * Unless explicitly stated otherwise all files in this repository are licensed
 * under the Apache License Version 2.0.
 *
 * This product includes software developed at Datadog (https://www.datadoghq.com/).
 * Copyright 2021 Datadog, Inc.
 */

package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetMetricDifferentTagOrder(t *testing.T) {

	tm := time.Now()
	batcher := MakeBatcher(10)
	dm1 := Distribution{
		Name:   "metric-1",
		Values: []MetricValue{{Timestamp: tm, Value: 1}, {Timestamp: tm, Value: 2}},
		Tags:   []string{"a", "b", "c"},
	}
	dm2 := Distribution{
		Name:   "metric-1",
		Values: []MetricValue{{Timestamp: tm, Value: 3}, {Timestamp: tm, Value: 4}},
		Tags:   []string{"c", "b", "a"},
	}

	batcher.AddMetric(&dm1)
	batcher.AddMetric(&dm2)

	assert.Equal(t, []MetricValue{{Timestamp: tm, Value: 1}, {Timestamp: tm, Value: 2}, {Timestamp: tm, Value: 3}, {Timestamp: tm, Value: 4}}, dm1.Values)
}

func TestGetMetricFailDifferentName(t *testing.T) {

	tm := time.Now()
	batcher := MakeBatcher(10)

	dm1 := Distribution{
		Name:   "metric-1",
		Values: []MetricValue{{Timestamp: tm, Value: 1}, {Timestamp: tm, Value: 2}},
		Tags:   []string{"a", "b", "c"},
	}
	dm2 := Distribution{
		Name:   "metric-2",
		Values: []MetricValue{{Timestamp: tm, Value: 3}, {Timestamp: tm, Value: 4}},
		Tags:   []string{"c", "b", "a"},
	}

	batcher.AddMetric(&dm1)
	batcher.AddMetric(&dm2)

	assert.Equal(t, []MetricValue{{Timestamp: tm, Value: 1}, {Timestamp: tm, Value: 2}}, dm1.Values)

}

func TestGetMetricFailDifferentHost(t *testing.T) {
	tm := time.Now()
	batcher := MakeBatcher(10)

	host1 := "my-host-1"
	host2 := "my-host-2"

	dm1 := Distribution{
		Values: []MetricValue{{Timestamp: tm, Value: 1}, {Timestamp: tm, Value: 2}},

		Tags: []string{"a", "b", "c"},
		Host: &host1,
	}
	dm2 := Distribution{
		Name:   "metric-1",
		Values: []MetricValue{{Timestamp: tm, Value: 3}, {Timestamp: tm, Value: 4}},
		Tags:   []string{"a", "b", "c"},
		Host:   &host2,
	}

	batcher.AddMetric(&dm1)
	batcher.AddMetric(&dm2)

	assert.Equal(t, []MetricValue{{Timestamp: tm, Value: 1}, {Timestamp: tm, Value: 2}}, dm1.Values)
}

func TestGetMetricSameHost(t *testing.T) {

	tm := time.Now()
	batcher := MakeBatcher(10)

	host := "my-host"

	dm1 := Distribution{
		Name:   "metric-1",
		Values: []MetricValue{{Timestamp: tm, Value: 1}, {Timestamp: tm, Value: 2}},
		Tags:   []string{"a", "b", "c"},
		Host:   &host,
	}
	dm2 := Distribution{
		Name:   "metric-1",
		Values: []MetricValue{{Timestamp: tm, Value: 3}, {Timestamp: tm, Value: 4}},
		Tags:   []string{"a", "b", "c"},
		Host:   &host,
	}

	batcher.AddMetric(&dm1)
	batcher.AddMetric(&dm2)

	assert.Equal(t, []MetricValue{{Timestamp: tm, Value: 1}, {Timestamp: tm, Value: 2}, {Timestamp: tm, Value: 3}, {Timestamp: tm, Value: 4}}, dm1.Values)
}

func TestToAPIMetricsSameInterval(t *testing.T) {
	tm := time.Now()
	hostname := "host-1"

	batcher := MakeBatcher(10)
	dm := Distribution{
		Name:   "metric-1",
		Tags:   []string{"a", "b", "c"},
		Host:   &hostname,
		Values: []MetricValue{},
	}

	dm.AddPoint(tm, 1)
	dm.AddPoint(tm, 2)
	dm.AddPoint(tm, 3)

	batcher.AddMetric(&dm)

	floatTime := float64(tm.Unix())
	result := batcher.ToAPIMetrics()
	expected := []APIMetric{
		{
			Name:       "metric-1",
			Host:       &hostname,
			Tags:       []string{"a", "b", "c"},
			MetricType: DistributionType,
			Interval:   nil,
			Points: []interface{}{
				[]interface{}{floatTime, []interface{}{float64(1)}},
				[]interface{}{floatTime, []interface{}{float64(2)}},
				[]interface{}{floatTime, []interface{}{float64(3)}},
			},
		},
	}

	assert.Equal(t, expected, result)
}
