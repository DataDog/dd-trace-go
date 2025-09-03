// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package rum

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInjector(t *testing.T) {
	payload := []byte("Hello, world!")
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}
	injector := NewInjector(h)
	server := httptest.NewServer(injector)
	defer server.Close()

	resp, err := http.DefaultClient.Get(server.URL)
	assert.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, "Hello, world!", string(respBody))
	// TODO: when Content-Length is implemented, uncomment this.
	// assert.Equal(t, int64(len(payload) + len(snippet)), resp.ContentLength)
}

func TestInjectorMatch(t *testing.T) {
	cases := []struct {
		in   []byte
		want state
	}{
		{[]byte("hello </head> world"), sDone},
		{[]byte("noise </   head     > tail"), sDone}, // spaces after '/' and before '>'
		{[]byte("nope < /head>"), sInit},              // space between '<' and '/'
		{[]byte("nope </ he ad >"), sInit},            // spaces inside "head"
		{[]byte("ok </\tHead\t\t   >"), sDone},        // tabs after '/', spaces before '>'
		{[]byte("partial </hea>"), sInit},             // missing 'd'
		{[]byte("wrong </header>"), sInit},            // extra letters before '>'
		{[]byte("caps </HEAD>"), sDone},               // case-insensitive
		{[]byte("attr-like </head foo>"), sInit},      // rejected by our custom rule
		{[]byte("prefix << /   h e a d  >"), sInit},   // multiple violations
	}

	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			i := &injector{}
			i.match(tc.in)
			got := i.st
			i.Reset()
			if got != tc.want {
				t.Fatalf("match(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestInjectorWrite(t *testing.T) {
	cases := []struct {
		category string
		in       string // comma separated chunks
		out      string
	}{
		{"basic", "abc</head>def", "abc<snippet></head>def"},
		{"basic", "abc</he,ad>def", "abc<snippet></head>def"},
		{"basic", "abc,</head>def", "abc<snippet></head>def"},
		{"basic", "abc</head>,def", "abc<snippet></head>def"},
		{"basic", "abc</h,ea,d>def", "abc<snippet></head>def"},
		{"basic", "abc,</hea,d>def", "abc<snippet></head>def"},
		{"no-head", "abc", "abc"},
		{"no-head", "abc</hea", "abc</hea"},
		{"empty", "", ""},
		{"empty", ",", ""},
		{"incomplete", "abc</he</head>def", "abc</he<snippet></head>def"},
		{"incomplete", "abc</he,</head>def", "abc</he<snippet></head>def"},
		{"casing", "abc</HeAd>def", "abc<snippet></HeAd>def"},
		{"casing", "abc</HEAD>def", "abc<snippet></HEAD>def"},
		{"spaces", "abc </head>def", "abc <snippet></head>def"},
		{"spaces", "abc </hea,d>def", "abc <snippet></head>def"},
		{"spaces", "abc</ head>def", "abc<snippet></ head>def"},
		{"spaces", "abc</h ead>def", "abc</h ead>def"},
		{"spaces", "abc</he ad>def", "abc</he ad>def"},
		{"spaces", "abc</hea d>def", "abc</hea d>def"},
		{"spaces", "abc</head >def", "abc<snippet></head >def"},
		{"spaces", "abc</head> def", "abc<snippet></head> def"},
		// {"comment", "<!-- </head>", "<!-- </head>"}, // TODO: don't inject if </head> is found in a comment
	}

	for _, tc := range cases {
		t.Run(tc.category+":"+tc.in, func(t *testing.T) {
			total := 0
			recorder := httptest.NewRecorder()
			i := &injector{
				wrapped: recorder,
			}
			chunks := strings.Split(tc.in, ",")
			for _, chunk := range chunks {
				w, err := i.Write([]byte(chunk))
				assert.NoError(t, err)
				total += w
			}
			sz, err := i.Flush()
			assert.NoError(t, err)
			total += sz
			body := recorder.Body.String()
			assert.Equal(t, tc.out, body)
			assert.Equal(t, len(tc.out), total)
		})
	}
}

type sinkResponseWriter struct {
	out []byte
}

func (s *sinkResponseWriter) Header() http.Header {
	return http.Header{}
}
func (s *sinkResponseWriter) Write(p []byte) (int, error) {
	s.out = append(s.out, p...)
	return len(p), nil
}
func (s *sinkResponseWriter) WriteHeader(int) {}

func BenchmarkInjectorWrite(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	sink := &sinkResponseWriter{}
	ij := &injector{
		wrapped: sink,
	}
	for i := 0; i < b.N; i++ {
		ij.Write([]byte("abc</head>def"))
		if !bytes.Equal(sink.out, []byte("abc<snippet></head>def")) {
			b.Fatalf("out is not as expected: %q", sink.out)
		}
		sink.out = sink.out[:0]
		ij.Reset()
	}
}

func FuzzInjectorWrite(f *testing.F) {
	cases := []string{
		"abc</head>def",
		"abc",
		"abc</hea",
		"abc</he</head>def",
		"abc</HeAd>def",
		"abc</HEAD>def",
		"abc </head>def",
		"abc</ head>def",
		"abc</h ead>def",
		"abc</he ad>def",
		"abc</hea d>def",
		"abc</head >def",
		"abc</head> def",
		"",
	}
	for _, tc := range cases {
		f.Add([]byte(tc))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		sink := &sinkResponseWriter{}
		ij := &injector{
			wrapped: sink,
		}
		ij.Write(in)
	})
}
