package ring

import "testing"

const benchCap = 600 // matches default MaxAge/Every (1min / 100ms)

func BenchmarkBuffer_PushNoWrap(b *testing.B) {
	for b.Loop() {
		buf := NewBuffer[int](b.N + 1)
		for i := range b.N {
			buf.Push(i)
		}
	}
}

func BenchmarkBuffer_PushWrap(b *testing.B) {
	buf := NewBuffer[int](benchCap)
	b.ResetTimer()
	for i := range b.N {
		buf.Push(i)
	}
}

func BenchmarkBuffer_Slice(b *testing.B) {
	buf := NewBuffer[int](benchCap)
	for i := range benchCap {
		buf.Push(i)
	}
	b.ResetTimer()
	for range b.N {
		_ = buf.Slice()
	}
}

func BenchmarkBuffer_PushAndSlice(b *testing.B) {
	buf := NewBuffer[int](benchCap)
	b.ResetTimer()
	for i := range b.N {
		buf.Push(i)
		if i%benchCap == 0 {
			_ = buf.Slice()
		}
	}
}
