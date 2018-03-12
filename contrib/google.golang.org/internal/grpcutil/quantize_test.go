package grpcutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQuantizeResource(t *testing.T) {
	assert := assert.New(t)

	for _, tt := range []struct {
		method, service      string
		resource, expService string
	}{
		{
			method:     "/pb.service/method",
			resource:   "method",
			service:    "my-service",
			expService: "my-service",
		},
		{
			method:     "/pb.service/method",
			resource:   "method",
			expService: "service",
		},
		{
			method:     "/service/method",
			resource:   "method",
			expService: "service",
		},
		{
			method:     "pb.service/method",
			resource:   "method",
			expService: "pb.service",
		},
		{
			method:     "pb.service/method",
			resource:   "method",
			service:    "my-service",
			expService: "my-service",
		},
		{
			method:   "abcdef",
			resource: "abcdef",
		},
	} {
		s, r := QuantizeResource(tt.service, tt.method)

		assert.Equal(tt.expService, s)
		assert.Equal(tt.resource, r)
	}
}
