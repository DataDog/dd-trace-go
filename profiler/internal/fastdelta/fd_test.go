package fastdelta

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/richardartoul/molecule"
	"github.com/richardartoul/molecule/src/protowire"
	"github.com/stretchr/testify/require"
)

const heapFile = "heap.pprof"
const bigHeapFile = "big-heap.pprof"

func BenchmarkFastDelta(b *testing.B) {
	for _, f := range []string{heapFile, bigHeapFile} {
		testFile := "testdata/" + f
		b.Run(testFile, func(b *testing.B) {
			before, err := os.ReadFile(testFile)
			if err != nil {
				b.Fatal(err)
			}
			after, err := os.ReadFile(testFile)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()

			buf := new(bytes.Buffer)
			for i := 0; i < b.N; i++ {
				buf.Reset()
				dc := NewDeltaComputer("alloc_objects", "alloc_space")
				err := dc.Delta(before, io.Discard)
				if err != nil {
					b.Fatal(err)
				}
				err = dc.Delta(after, buf)
				if err != nil {
					b.Fatal(err)
				}
				sink = buf.Bytes()
			}
		})
	}
}

func BenchmarkMakeGolden(b *testing.B) {
	for _, f := range []string{heapFile, bigHeapFile} {
		testFile := "testdata/" + f
		b.Run(testFile, func(b *testing.B) {
			before, err := os.ReadFile(testFile)
			if err != nil {
				b.Fatal(err)
			}
			after, err := os.ReadFile(testFile)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				psink = makeGolden(b, before, after, []string{"alloc_objects", "alloc_space"})
			}
		})
	}
}

var sink []byte
var psink *profile.Profile

func TestFastDeltaComputer(t *testing.T) {
	tests := []struct {
		Name   string
		Before string
		After  string
		Fields []string
	}{
		{
			Name:   "heap",
			Before: "testdata/heap.before.pprof",
			After:  "testdata/heap.after.pprof",
			Fields: []string{"alloc_objects", "alloc_space"},
		},
		{
			Name:   "block",
			Before: "testdata/block.before.pprof",
			After:  "testdata/block.after.pprof",
			Fields: []string{"contentions", "delay"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			before, err := os.ReadFile(tc.Before)
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(tc.After)
			if err != nil {
				t.Fatal(err)
			}

			dc := NewDeltaComputer(tc.Fields...)
			if err := dc.Delta(before, io.Discard); err != nil {
				t.Fatal(err)
			}
			// TODO: check the output of the first Delta. Should be unchanged

			data := new(bytes.Buffer)
			if err := dc.Delta(after, data); err != nil {
				t.Fatal(err)
			}

			delta, err := profile.ParseData(data.Bytes())
			if err != nil {
				t.Fatalf("parsing delta profile: %s", err)
			}

			golden := makeGolden(t, before, after, tc.Fields)

			golden.Scale(-1)
			diff, err := profile.Merge([]*profile.Profile{delta, golden})
			if err != nil {
				t.Fatal(err)
			}
			if len(diff.Sample) != 0 {
				t.Errorf("non-empty diff from golden vs delta: %v", diff)
				t.Errorf("got: %v", delta)
				t.Errorf("want: %v", golden)
			}
		})
	}
}

func makeGolden(t testing.TB, before, after []byte, fields []string) *profile.Profile {
	t.Helper()
	b, err := profile.ParseData(before)
	if err != nil {
		t.Fatal(err)
	}
	a, err := profile.ParseData(after)
	if err != nil {
		t.Fatal(err)
	}

	ratios := make([]float64, len(b.SampleType))
	for i, v := range b.SampleType {
		for _, f := range fields {
			if f == v.Type {
				ratios[i] = -1
			}
		}
	}
	if err := b.ScaleN(ratios); err != nil {
		t.Fatal(err)
	}

	c, err := profile.Merge([]*profile.Profile{b, a})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCompaction(t *testing.T) {
	// given

	bigHeapBytes, err := os.ReadFile("testdata/big-heap.pprof")
	require.NoError(t, err)
	zeroDeltaPprof, err := profile.ParseData(bigHeapBytes)
	require.NoError(t, err)
	// add some string values
	zeroDeltaPprof.Comments = []string{"hello", "world"}
	zeroDeltaPprof.DefaultSampleType = "inuse_objects"
	zeroDeltaPprof.DropFrames = "drop 'em"
	zeroDeltaPprof.KeepFrames = "keep 'em"

	zeroDeltaBuf := &bytes.Buffer{}
	require.NoError(t, zeroDeltaPprof.WriteUncompressed(zeroDeltaBuf))

	dc := NewDeltaComputer("alloc_objects", "alloc_space", "inuse_objects", "inuse_space")
	buf := new(bytes.Buffer)
	err = dc.Delta(zeroDeltaBuf.Bytes(), buf)
	zeroDeltaBytes := buf.Bytes()
	require.NoError(t, err)
	require.Len(t, zeroDeltaBytes, zeroDeltaBuf.Len())

	// when

	// create a value delta
	require.NoError(t, err)
	zeroDeltaPprof.Sample[0].Value[0] += 42
	bufNext := &bytes.Buffer{}
	require.NoError(t, zeroDeltaPprof.WriteUncompressed(bufNext))
	buf.Reset()
	err = dc.Delta(bufNext.Bytes(), buf)
	delta := buf.Bytes()
	require.NoError(t, err)
	firstDeltaPprof, err := profile.ParseData(delta)
	require.NoError(t, err)

	// then

	require.Len(t, firstDeltaPprof.Sample, 1, "Only one expected sample")
	require.Len(t, firstDeltaPprof.Mapping, 1, "Only one expected mapping")
	require.Len(t, firstDeltaPprof.Location, 3, "Location should be GCd")
	require.Len(t, firstDeltaPprof.Function, 3, "Function should be GCd")
	require.Equal(t, int64(42), firstDeltaPprof.Sample[0].Value[0])

	// make sure we shrunk the string table too (85K+ without pruning)
	// note that most of the delta buffer is full of empty strings, highly compressible
	require.Less(t, len(delta), 3720)

	// string table checks on Profile message string fields
	require.Equal(t, []string{"hello", "world"}, firstDeltaPprof.Comments)
	require.Equal(t, "inuse_objects", firstDeltaPprof.DefaultSampleType)
	require.Equal(t, "drop 'em", firstDeltaPprof.DropFrames)
	require.Equal(t, "keep 'em", firstDeltaPprof.KeepFrames)

	// check a mapping
	m := firstDeltaPprof.Mapping[0]
	require.Equal(t, "537aaf6df5ba3cc343a7c78738e4fe3890ab9782", m.BuildID)
	require.Equal(t, "/usr/local/bin/nicky", m.File)

	// check a value type
	vt := firstDeltaPprof.SampleType[0]
	require.Equal(t, "alloc_objects", vt.Type)
	require.Equal(t, "count", vt.Unit)

	// check a function
	f := firstDeltaPprof.Sample[0].Location[0].Line[0].Function
	require.Equal(t, "hawaii-alabama-artist", f.SystemName)
	require.Equal(t, "hawaii-alabama-artist", f.Name)
	require.Equal(t, "/wisconsin/video/beer/spring/delta/pennsylvania/four", f.Filename)

	// check a label
	l := firstDeltaPprof.Sample[0].NumLabel
	require.Contains(t, l, "bytes")
}

func TestSampleHashingConsistency(t *testing.T) {
	// f builds a profile with a single sample which has labels in the given
	// order. We build the profile ourselves because we can control the
	// precise binary encoding of the profile.
	f := func(labels ...string) []byte {
		b := new(bytes.Buffer)
		ps := molecule.NewProtoStream(b)
		ps.Embedded(1, func(ps *molecule.ProtoStream) error {
			// sample_type
			ps.Int64(1, 1) // type
			ps.Int64(2, 2) // unit
			return nil
		})
		ps.Embedded(11, func(ps *molecule.ProtoStream) error {
			// period_type
			ps.Int64(1, 1) // type
			ps.Int64(2, 2) // unit
			return nil
		})
		ps.Int64(12, 1) // period
		ps.Int64(9, 1)  // time_nanos
		ps.Embedded(4, func(ps *molecule.ProtoStream) error {
			// location
			ps.Uint64(1, 1)    // location ID
			ps.Uint64(2, 1)    // mapping ID
			ps.Uint64(3, 0x42) // address
			return nil
		})
		ps.Embedded(2, func(ps *molecule.ProtoStream) error {
			// samples
			ps.Uint64(1, 1) // location ID
			ps.Uint64(2, 1) // value
			for i := 0; i < len(labels); i += 2 {
				ps.Embedded(3, func(ps *molecule.ProtoStream) error {
					ps.Uint64(1, uint64(i)+3) // key strtab offset
					ps.Uint64(2, uint64(i)+4) // str strtab offset
					return nil
				})
			}
			return nil
		})
		ps.Embedded(3, func(ps *molecule.ProtoStream) error {
			// mapping
			ps.Uint64(1, 1) // ID
			return nil
		})
		// don't need functions
		buf := b.Bytes()
		writeString := func(s string) {
			buf = protowire.AppendVarint(buf, (6<<3)|2)
			buf = protowire.AppendVarint(buf, uint64(len(s)))
			buf = append(buf, s...)
		}
		writeString("")     // 0 -- molecule doesn't let you write 0-length with ProtoStream
		writeString("type") // 1
		writeString("unit") // 2
		for i := 0; i < len(labels); i += 2 {
			writeString(labels[i])
			writeString(labels[i+1])
		}
		return buf
	}
	a := f("foo", "bar", "abc", "123")
	b := f("abc", "123", "foo", "bar")

	// double-checks that our generated profiles are valid
	require.NotEqual(t, a, b)
	_, err := profile.ParseData(a)
	require.NoError(t, err)
	_, err = profile.ParseData(b)
	require.NoError(t, err)

	dc := NewDeltaComputer("type")
	err = dc.Delta(a, io.Discard)
	require.NoError(t, err)
	buf := new(bytes.Buffer)
	err = dc.Delta(b, buf)
	require.NoError(t, err)

	p, err := profile.ParseData(buf.Bytes())
	require.NoError(t, err)
	// There should be no samples because we didn't actually change the
	// profile, just the order of the labels.
	require.Empty(t, p.Sample)
}
