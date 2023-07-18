package internal

type LockMap interface {
	Iter(func(key, val string))
	Len() int
	Clear()
}

// ReadOnlyLockMap is a read only version of LockMap which avoids using mutexes as the caller guarantees no writes.
type ReadOnlyLockMap struct {
	m map[string]string
}

func NewReadOnlyLockMap(m map[string]string) *ReadOnlyLockMap {
	return &ReadOnlyLockMap{m: m}
}

func (r *ReadOnlyLockMap) Iter(f func(key string, val string)) {
	for k, v := range r.m {
		f(k, v)
	}
}

func (r *ReadOnlyLockMap) Len() int {
	return len(r.m)
}

// Clear does panics as it is not safe to clear a ReadOnly map
func (r *ReadOnlyLockMap) Clear() {
	panic("Not safe to call Clear on a ReadOnlyLockMap")
}
