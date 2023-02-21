// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// This file is exactly pulled from datadog-agent/pkg/util/cachedfetch only changing the logger

package cachedfetch

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// If Attempt never succeeds, f.Fetch returns an error
func TestFetcherNeverSucceeds(t *testing.T) {
	f := Fetcher{
		Attempt: func(ctx context.Context) (string, error) { return "", fmt.Errorf("uhoh") },
	}

	v, err := f.Fetch(context.TODO())
	require.Empty(t, v)
	require.Error(t, err)

	v, err = f.Fetch(context.TODO())
	require.Empty(t, v)
	require.Error(t, err)
}

// Each call to f.Fetch() calls Attempt again
func TestFetcherCalledEachFetch(t *testing.T) {
	count := 0
	f := Fetcher{
		Attempt: func(ctx context.Context) (string, error) {
			count++
			return strconv.Itoa(count), nil
		},
	}

	v, err := f.Fetch(context.TODO())
	require.Equal(t, "1", v)
	require.NoError(t, err)

	v, err = f.Fetch(context.TODO())
	require.Equal(t, "2", v)
	require.NoError(t, err)
}

// After a successful call, f.Fetch does not fail
func TestFetcherUsesCachedValue(t *testing.T) {
	count := 0
	f := Fetcher{
		Name: "test",
		Attempt: func(ctx context.Context) (string, error) {
			count++
			if count%2 == 0 {
				return "", fmt.Errorf("uhoh")
			}
			return strconv.Itoa(count), nil
		},
	}

	for iter, exp := range []string{"1", "1", "3", "3", "5", "5"} {
		v, err := f.Fetch(context.TODO())
		require.Equal(t, exp, v, "on iteration %d", iter)
		require.NoError(t, err)
	}
}

// Errors are logged with LogFailure
func TestFetcherLogsWhenUsingCached(t *testing.T) {
	count := 0
	errs := []string{}
	f := Fetcher{
		Attempt: func(ctx context.Context) (string, error) {
			count++
			if count%2 == 0 {
				return "", fmt.Errorf("uhoh")
			}
			return strconv.Itoa(count), nil
		},
		LogFailure: func(err error, v interface{}) {
			errs = append(errs, fmt.Sprintf("%v, %v", err, v))
		},
	}

	for iter, exp := range []string{"1", "1", "3", "3"} {
		v, err := f.Fetch(context.TODO())
		require.Equal(t, exp, v, "on iteration %d", iter)
		require.NoError(t, err)
	}

	require.Equal(t, []string{"uhoh, 1", "uhoh, 3"}, errs)
}

func TestReset(t *testing.T) {
	succeed := func(ctx context.Context) (string, error) { return "yay", nil }
	fail := func(ctx context.Context) (string, error) { return "", fmt.Errorf("uhoh") }
	f := Fetcher{}

	f.Attempt = succeed
	v, err := f.Fetch(context.TODO())
	require.Equal(t, "yay", v)
	require.NoError(t, err)

	f.Attempt = fail
	v, err = f.Fetch(context.TODO())
	require.Equal(t, "yay", v)
	require.NoError(t, err)

	f.Reset()

	v, err = f.Fetch(context.TODO())
	require.Equal(t, "", v)
	require.Error(t, err)
}
