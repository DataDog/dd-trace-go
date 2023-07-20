// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package elastic

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	elastictrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/olivere/elastic"
	elasticv3 "gopkg.in/olivere/elastic.v3"
	elasticv5 "gopkg.in/olivere/elastic.v5"
)

const (
	elasticV5URL   = "http://127.0.0.1:9201"
	elasticV3URL   = "http://127.0.0.1:9200"
	elasticFakeURL = "http://127.0.0.1:29201"
)

type Integration struct {
	c5       *elasticv5.Client
	c3       *elasticv3.Client
	numSpans int
	opts     []elastictrace.ClientOption
}

func New() *Integration {
	return &Integration{
		opts: make([]elastictrace.ClientOption, 0),
	}
}

func (i *Integration) Name() string {
	return "olivere/elastic"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	var err error
	tc := elastictrace.NewHTTPClient(i.opts...)
	i.c5, err = elasticv5.NewClient(
		elasticv5.SetURL(elasticV5URL),
		elasticv5.SetHttpClient(tc),
		elasticv5.SetSniff(false),
		elasticv5.SetHealthcheck(false),
	)
	require.NoError(t, err)

	i.c3, err = elasticv3.NewClient(
		elasticv3.SetURL(elasticV3URL),
		elasticv3.SetHttpClient(tc),
		elasticv3.SetSniff(false),
		elasticv3.SetHealthcheck(false),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	var err error
	_, err = i.c5.Index().
		Index("twitter").Id("1").
		Type("tweet").
		BodyString(`{"user": "test", "message": "hello"}`).
		Do(context.TODO())
	require.NoError(t, err)
	i.numSpans++

	_, err = i.c5.Get().Index("twitter").Type("tweet").
		Id("1").Do(context.TODO())
	require.NoError(t, err)
	i.numSpans++

	_, err = i.c5.Get().Index("not-real-index").
		Id("1").Do(context.TODO())
	require.NoError(t, err)
	i.numSpans++

	_, err = i.c3.Index().
		Index("twitter").Id("1").
		Type("tweet").
		BodyString(`{"user": "test", "message": "hello"}`).
		DoC(context.TODO())
	require.NoError(t, err)
	i.numSpans++

	_, err = i.c3.Get().Index("twitter").Type("tweet").
		Id("1").DoC(context.TODO())
	require.NoError(t, err)
	i.numSpans++

	_, err = i.c3.Get().Index("not-real-index").
		Id("1").DoC(context.TODO())
	require.NoError(t, err)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, elastictrace.WithServiceName(name))
}
