package tracer

import "testing"

func BenchmarkNormalTimeNow(b *testing.B) {
	for n := 0; n < b.N; n++ {
		lowPrecisonNow()
	}
}

func BenchmarkHighPrecisionTime(b *testing.B) {
	for n := 0; n < b.N; n++ {
		highPrecisionNow()
	}
}
