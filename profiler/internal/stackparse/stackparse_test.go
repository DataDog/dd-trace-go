package stackparse

import (
	"bytes"
	"io/ioutil"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	data, err := ioutil.ReadFile("paul.txt")
	require.NoError(t, err)

	goroutines, err := Parse(bytes.NewReader(data))
	require.NoError(t, err)

	require.Equal(t, 3762, len(goroutines))
	require.Equal(t, goroutines[0].ID, 117227920)
}

func BenchmarkParse(b *testing.B) {
	data, err := ioutil.ReadFile("paul.txt")
	require.NoError(b, err)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := Parse(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
	}
}
