package grpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPeerServiceFromMethod(t *testing.T) {
	for _, testCase := range []struct {
		method   string
		expected string
	}{
		{"", ""},
		{"/", ""},
		{"//", ""},
		{"foo/", "foo"},
		{"/foo", "foo"},
		{"/foo/", "foo"},
		{"/foo/bar", "foo"},
		{"/examplepb.ExampleService/Query", "examplepb.ExampleService"},
	} {
		t.Run("", func(t *testing.T) {
			assert.Equal(t, testCase.expected, peerServiceFromMethod(testCase.method))
		})
	}
}
