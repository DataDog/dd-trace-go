package memcache

import (
	"testing"

	memcachetrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/bradfitz/gomemcache/memcache"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/stretchr/testify/require"
)

type Integration struct {
	cl       *memcachetrace.Client
	numSpans int
}

func New() *Integration {
	return &Integration{}
}

func (i *Integration) Name() string {
	return "contrib/bradfitz/gomemcache/memcache"
}

func (i *Integration) Init(_ *testing.T) {
	i.cl = memcachetrace.WrapClient(memcache.New("127.0.0.1:11211"))
}

func (i *Integration) GenSpans(t *testing.T) {
	err := i.cl.Set(&memcache.Item{Key: "myKey", Value: []byte("myValue")})
	require.NoError(t, err)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}
