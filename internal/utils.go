package internal

func FoldM[T, V any, K comparable](acc T, f func(acc T, k K, v V) T, m map[K]V) T {
	for key, val := range m {
		acc = f(acc, key, val)
	}
	return acc
}
