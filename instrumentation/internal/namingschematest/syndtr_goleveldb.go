package namingschematest

import (
	"testing"

	leveldbtrace "github.com/DataDog/dd-trace-go/contrib/syndtr/goleveldb/v2/leveldb"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/storage"
)

func syndtrGoLevelDBGenSpans() harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []leveldbtrace.Option
		if serviceOverride != "" {
			opts = append(opts, leveldbtrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		db, err := leveldbtrace.Open(storage.NewMemStorage(), &opt.Options{}, opts...)
		require.NoError(t, err)
		defer db.Close()
		err = db.Put([]byte("key"), []byte("value"), nil)
		require.NoError(t, err)

		return mt.FinishedSpans()
	}
}

var syndtrGoLevelDB = harness.TestCase{
	Name:     instrumentation.PackageSyndtrGoLevelDB,
	GenSpans: syndtrGoLevelDBGenSpans(),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"leveldb"},
		DDService:       []string{"leveldb"},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "leveldb.query", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "leveldb.query", spans[0].OperationName())
	},
}
