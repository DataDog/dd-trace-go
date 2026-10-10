// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package rediswrap holds the client-version-independent machinery behind the
// go-redis WrapClient idempotency implementations: the weakly keyed registry,
// the goroutine-scoped reentrancy marks, and the reflection helpers that walk
// client structs. The go-redis integrations differ only in the redis library
// versions they compile against; the deduplication logic below is expressed
// over any-typed clients and members so all four modules share one copy.
package rediswrap

import (
	"encoding/binary"
	"reflect"
	"runtime"
	"sync"
	"time"
	"unsafe"
	"weak"
)

// Handle is a weak identity for any pointer client: two handles compare equal
// exactly for the same object, and the handle never keeps it alive.
type Handle = weak.Pointer[byte]

// HandleOf returns a weak identity for any pointer client, by referencing the
// start of the object it points to, or reports that the client cannot be
// keyed.
func HandleOf(client any) (Handle, bool) {
	v := reflect.ValueOf(client)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return Handle{}, false
	}
	return weak.Make((*byte)(v.UnsafePointer())), true
}

// Goid returns the current goroutine's ID, from the header of its stack
// snapshot. It identifies the goroutine that started an in-flight install, so
// a call chain re-entering its own install does not wait for it while
// concurrent installs are still waited on.
func Goid() uint64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	// The first line reads "goroutine 123 [running]:".
	if len(b) < 11 || string(b[:10]) != "goroutine " {
		return 0
	}
	var id uint64
	for _, c := range b[10:] {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + uint64(c-'0')
	}
	return id
}

// walkGuard marks a WrapClient call in progress for one client: its unlocked
// AddHook — user-controlled code mutating the proxy's fields — must not
// overlap another call's reflective field walk over those same fields.
type walkGuard struct {
	goid uint64
	done chan struct{}
}

// walking records the in-progress WrapClient call per client, keyed weakly.
var walking sync.Map // Handle -> *walkGuard

// BeginWalk serializes WrapClient calls for one client: it waits for a
// concurrent call for the same client — including its unlocked,
// user-controlled AddHook — to complete, then marks this call as the one
// in progress. A call on the same goroutine (an AddHook re-entering
// WrapClient) never waits. The wait is bounded: a user callback that
// delegates to another goroutine — starting one that calls WrapClient for
// this very client and waiting for it — must not deadlock against the
// guard, and the in-flight call instruments the client either way, so a
// wait that outlives the bound reports false and the caller returns
// without wrapping. The returned function must be called when the
// WrapClient call ends, and only when ok is true.
func BeginWalk(key Handle, wait time.Duration) (release func(), ok bool) {
	id := Goid()
	for {
		v, ok := walking.Load(key)
		if !ok {
			g := &walkGuard{goid: id, done: make(chan struct{})}
			if _, loaded := walking.LoadOrStore(key, g); !loaded {
				return func() {
					close(g.done)
					walking.Delete(key)
				}, true
			}
			continue // someone else registered; re-check
		}
		g := v.(*walkGuard)
		if g.goid == id {
			// The guard belongs to this goroutine — an AddHook re-entering
			// WrapClient; it is already serialized.
			return func() {}, true
		}
		select {
		case <-g.done:
		case <-time.After(wait):
			// The holder did not finish within the wait: it instruments the
			// client, and a nested call synchronously waiting on it must not
			// block it forever.
			return func() {}, false
		}
	}
}

// Unguarded reports whether s is a struct copy whose mutex field cannot be
// taken: a value mutex of a non-addressable struct — a proxy passed by
// value — locks nothing, because the state its fields reach through maps,
// slices, and pointers is the original's, guarded by the original's mutex.
// An unexported pointer mutex on such a copy cannot even be read to be
// taken, and counts the same. Readers must not treat such a copy's
// interiors as safe; its own header fields are snapshots and stay
// readable.
func Unguarded(s reflect.Value) bool {
	if s.Kind() != reflect.Struct || s.CanAddr() {
		return false
	}
	for i := 0; i < s.NumField(); i++ {
		t := s.Type().Field(i).Type
		if t == mutexType || t == rwMutexType {
			return true
		}
		if t == mutexPointerType || t == rwMutexPointerType {
			// A pointer mutex hangs off the field value itself: an exported
			// one can be taken without the field's address, but an
			// unexported one on a copy cannot even be read — LockStruct
			// skips it, and the shared state it guards must not be
			// descended into.
			if s.Field(i).CanInterface() {
				continue
			}
			return true
		}
	}
	return false
}

// Mark identifies a proxy whose AddHook a WrapClient call on this goroutine is
// currently running: an AddHook that re-enters WrapClient for the same proxy
// must recognize its own installation instead of recursing. The members make a
// non-comparable value proxy — which has neither a weak pointer identity nor
// a comparable value — recognizable through the concrete clients it delegates
// to. Refs adds the addresses its reference-bearing fields hold, so two
// non-comparable proxies of the same type over the same members — two lazy
// proxies, say — are not mistaken for each other when their fields point at
// distinct objects.
type Mark struct {
	Proxy   any
	Members []any
	Refs    []unsafe.Pointer
}

// RefIDs collects the addresses the value's reference-bearing fields hold —
// pointer fields, and interfaces holding pointers — through two levels of
// struct fields. Two values that store the same addresses share the objects
// those fields point at.
func RefIDs(v any) []unsafe.Pointer {
	return refIDs(reflect.ValueOf(v), 2)
}

func refIDs(v reflect.Value, depth int) []unsafe.Pointer {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			return []unsafe.Pointer{v.UnsafePointer()}
		}
	case reflect.Interface:
		if !v.IsNil() {
			return refIDs(v.Elem(), depth)
		}
	case reflect.Map, reflect.Slice:
		// A map or slice field is reference-bearing: the map's and the
		// backing array's addresses differ between two proxies that nothing
		// else distinguishes.
		if p := v.UnsafePointer(); p != nil {
			return []unsafe.Pointer{p}
		}
	case reflect.Struct:
		if depth == 0 {
			return nil
		}
		var ids []unsafe.Pointer
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !f.CanInterface() && f.CanAddr() {
				// Unexported fields still identify a proxy: a private
				// delegate or marker pointer differs between two proxies
				// that nothing else can tell apart. Addressable values —
				// a pointer receiver's struct, or the fields reached
				// through one — expose them; a plain value copy does not.
				f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
			}
			if !f.CanInterface() {
				continue
			}
			ids = append(ids, refIDs(f, depth-1)...)
		}
		return ids
	}
	return nil
}

// Installing records, per goroutine, the proxies currently being installed.
var Installing sync.Map // uint64 (goid) -> []Mark

// MarkInstalling records that this goroutine is about to run client's
// AddHook; the returned function must be called once it returns.
func MarkInstalling(client any, members []any) func() {
	id := Goid()
	var list []Mark
	if v, ok := Installing.Load(id); ok {
		list = v.([]Mark)
	}
	Installing.Store(id, append(list, Mark{Proxy: client, Members: members, Refs: RefIDs(client)}))
	return func() {
		id := Goid()
		v, ok := Installing.Load(id)
		if !ok {
			return
		}
		list := v.([]Mark)
		for i := len(list) - 1; i >= 0; i-- {
			if SameMark(list[i].Proxy, list[i].Members, list[i].Refs, client, members, RefIDs(client)) {
				list = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(list) == 0 {
			Installing.Delete(id)
		} else {
			Installing.Store(id, list)
		}
	}
}

// IsInstalling reports whether this goroutine is currently running the
// AddHook of client — a re-entrant call must not start its own installation.
func IsInstalling(client any, members []any) bool {
	id := Goid()
	v, ok := Installing.Load(id)
	if !ok {
		return false
	}
	for _, m := range v.([]Mark) {
		if SameMark(m.Proxy, m.Members, m.Refs, client, members, RefIDs(client)) {
			return true
		}
	}
	return false
}

// SameMark reports whether two proxies in installation marks identify the same
// client. Proxies with non-comparable dynamic types are matched through their
// member sets — the same concrete clients — rather than by value; the set
// comparison ignores order, since consecutive walks over a map-backed proxy
// can enumerate the same members differently.
func SameMark(pa any, ma []any, ra []unsafe.Pointer, pb any, mb []any, rb []unsafe.Pointer) bool {
	ta, tb := reflect.TypeOf(pa), reflect.TypeOf(pb)
	if ta == nil || tb == nil || ta != tb {
		return false
	}
	if ta.Comparable() {
		// Different comparable proxies are different, even over the same
		// members: only a proxy that cannot be compared at all falls back
		// to matching through its member set. A statically comparable type
		// may still hold non-comparable dynamic values behind interface
		// fields — comparing those panics — so the comparison runs under a
		// recovered panic, which only ever means "not equal".
		return safeEqual(pa, pb)
	}
	if len(ma) != len(mb) {
		return false
	}
	for _, a := range ma {
		found := false
		for _, b := range mb {
			if a == b {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(ra) != len(rb) {
		return false
	}
	for i := range ra {
		if ra[i] != rb[i] {
			return false
		}
	}
	return true
}

// MemberKeys returns weak handles for the members, in order; the caller's
// registry never pins them, and weak-pointer identity survives reclamation.
func MemberKeys(members []any) []Handle {
	keys := make([]Handle, 0, len(members))
	for _, member := range members {
		if k, ok := HandleOf(member); ok {
			keys = append(keys, k)
		}
	}
	return keys
}

// SameMembers reports whether the recorded member handles match the current
// members in order and count.
func SameMembers(recorded []Handle, members []any) bool {
	if len(recorded) != len(members) {
		return false
	}
	for i, member := range members {
		k, ok := HandleOf(member)
		if !ok || k != recorded[i] {
			return false
		}
	}
	return true
}

// ContainsKey reports whether the recorded member handles include key.
func ContainsKey(recorded []Handle, key Handle) bool {
	for _, k := range recorded {
		if k == key {
			return true
		}
	}
	return false
}

// CleanupMark records the clients a runtime cleanup is already attached to; it
// is keyed weakly and cleaned by that very cleanup, so it never pins a
// client.
var CleanupMark sync.Map // Handle -> struct{}

// RegisterCleanup attaches a cleanup to ptr's object once per client: a
// proxy re-observed after a delegate swap deletes and recreates its registry
// entry, and every recreation would otherwise attach another cleanup to the
// same object. The mark is removed by the cleanup itself. ptr must be a
// pointer; the caller passes the same object it took the weak handle from.
func RegisterCleanup[T any](ptr *T, cleanup func(Handle), key Handle) {
	if _, ok := CleanupMark.Load(key); ok {
		return
	}
	CleanupMark.Store(key, struct{}{})
	runtime.AddCleanup(ptr, func(k Handle) {
		cleanup(k)
		CleanupMark.Delete(k)
	}, key)
	// KeepAlive closes the window in which a GC could collect a client whose
	// last mention was the weak handle above.
	runtime.KeepAlive(ptr)
}

var (
	mutexType          = reflect.TypeOf(sync.Mutex{})
	rwMutexType        = reflect.TypeOf(sync.RWMutex{})
	mutexPointerType   = reflect.TypeFor[*sync.Mutex]()
	rwMutexPointerType = reflect.TypeFor[*sync.RWMutex]()
)

// LockStruct tries to take every mutex the struct owns, all-or-abort, and
// returns the unlock function: a proxy may replace its delegate fields while
// serving traffic, guarded by those mutexes, and reading them must not race
// with it. It reports false when any mutex stays held: the holder may be the
// caller's own goroutine, and blocking on it would deadlock — or, for the
// hook walkers, the chain is treated as transiently unreadable. Brief
// contention from another goroutine is ridden out with short retries. A
// struct without mutexes does not synchronize those fields, and reading them
// is then no more racy than the struct's own readers.
// safeEqual reports whether two statically comparable values are equal,
// treating a comparison that panics on non-comparable dynamic values — a
// map or slice behind an interface field — as inequality.
func safeEqual(a, b any) (eq bool) {
	defer func() {
		if recover() != nil {
			eq = false
		}
	}()
	return a == b
}

// HookState describes the outcome of trying to become the installer of a
// client's hook.
type HookState uint8

const (
	// HookBegin: this call is the installer; it must finish with EndHooking.
	HookBegin HookState = iota
	// HookSelfReentry: this goroutine is already installing for the client;
	// it must not install again.
	HookSelfReentry
	// HookOtherInstalling: another goroutine is installing; wait on the
	// returned mark's Done, then re-examine the client.
	HookOtherInstalling
)

// HookMark records one in-flight hook installation.
type HookMark struct {
	Goid uint64
	Done chan struct{}
}

// Hooking marks the clients whose hook installation is in flight, keyed by
// client handle: a client's own AddHook rebuilds its hook chain by calling
// every hook's constructors — user code, which may re-enter WrapClient — so
// the installation runs with the caller's package lock released, and a
// concurrent wrap of the same client must wait for it instead of racing a
// second hook onto the chain.
var Hooking sync.Map // Handle -> *HookMark

// TryBeginHooking records an in-flight hook installation for k on this
// goroutine, or reports why it cannot.
func TryBeginHooking(k Handle) (HookState, *HookMark) {
	mark := &HookMark{Goid: Goid(), Done: make(chan struct{})}
	for {
		v, loaded := Hooking.LoadOrStore(k, mark)
		if !loaded {
			return HookBegin, mark
		}
		existing := v.(*HookMark)
		if existing.Goid == Goid() {
			return HookSelfReentry, existing
		}
		return HookOtherInstalling, existing
	}
}

// EndHooking completes the installation mark recorded for k, releasing
// every waiter.
func EndHooking(k Handle, mark *HookMark) {
	Hooking.CompareAndDelete(k, mark)
	close(mark.Done)
}

// ValueHooking marks the value proxies whose hook installation is in
// flight, keyed by RefKey: a value proxy has no weak handle, but two
// concurrent wraps of it pass copies whose reference-bearing fields agree,
// so the key is shared between them and the install is serialized on it.
var ValueHooking sync.Map // string -> *HookMark

// TryBeginValueHooking records an in-flight hook installation for the value
// proxy keyed by key, or reports why it cannot.
func TryBeginValueHooking(key string) (HookState, *HookMark) {
	mark := &HookMark{Goid: Goid(), Done: make(chan struct{})}
	for {
		v, loaded := ValueHooking.LoadOrStore(key, mark)
		if !loaded {
			return HookBegin, mark
		}
		existing := v.(*HookMark)
		if existing.Goid == Goid() {
			return HookSelfReentry, existing
		}
		return HookOtherInstalling, existing
	}
}

// EndValueHooking completes the installation mark recorded for key,
// releasing every waiter.
func EndValueHooking(key string, mark *HookMark) {
	ValueHooking.CompareAndDelete(key, mark)
	close(mark.Done)
}

// RefKey returns a comparable identity for a value with no weak handle:
// its dynamic type and the addresses its reference-bearing fields hold. Two
// copies of the same value proxy share it — their fields agree — while
// distinct proxies holding distinct references do not. A value with no
// references at all has no identity to share, and the key is empty.
func RefKey(v any) string {
	refs := RefIDs(v)
	if len(refs) == 0 {
		return ""
	}
	b := make([]byte, 0, 16+len(refs)*8)
	b = append(b, reflect.TypeOf(v).String()...)
	for _, r := range refs {
		b = binary.LittleEndian.AppendUint64(b, uint64(uintptr(r)))
	}
	return string(b)
}

// IsMutexType reports whether t is one of the mutex types — a value or a
// pointer to a sync.Mutex or sync.RWMutex. Holder scans skip such fields:
// LockStruct has already taken them, and descending into one would
// re-acquire the same non-reentrant lock and read as self-inflicted
// contention.
func IsMutexType(t reflect.Type) bool {
	return t == mutexType || t == rwMutexType || t == mutexPointerType || t == rwMutexPointerType
}

func LockStruct(s reflect.Value) (unlock func(), ok bool) {
	var unlocks []func()
	// Two fields — pointer-pointer or value-pointer — may alias the same
	// mutex; locking it twice would deadlock the second TryLock and read as
	// contention. Each underlying lock is taken once.
	taken := make(map[unsafe.Pointer]struct{})
	for i := 0; i < s.NumField(); i++ {
		t := s.Type().Field(i).Type
		if t != mutexType && t != rwMutexType && t != mutexPointerType && t != rwMutexPointerType {
			continue
		}
		f := s.Field(i)
		if t == mutexPointerType || t == rwMutexPointerType {
			// The mutex is behind a pointer; its methods hang off the field
			// value itself.
			if f.IsNil() {
				continue
			}
			if !f.CanInterface() {
				// Unexported field: address it through its location.
				if !f.CanAddr() {
					continue
				}
				f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
			}
			if _, dup := taken[f.UnsafePointer()]; dup {
				continue
			}
			// The lock is taken through its concrete type, not through
			// reflect.Value.Call: a lock method invoked by reflection runs
			// outside the race detector's mutex bookkeeping, and a release
			// that races a caller's own lock use reads as a data race.
			// An RWMutex guards its fields against writers: a read lock
			// excludes them — the updates that race a field walk — while
			// allowing other readers, a caller holding its own read lock
			// included. An exclusive TryLock would report contention for
			// as long as any reader runs, and the walk would give up.
			var locked bool
			if t == rwMutexPointerType {
				mu := (*sync.RWMutex)(f.UnsafePointer())
				for range 100 {
					if mu.TryRLock() {
						locked = true
						break
					}
					time.Sleep(time.Millisecond)
				}
				if !locked {
					for _, u := range unlocks {
						u()
					}
					return func() {}, false
				}
				taken[f.UnsafePointer()] = struct{}{}
				unlocks = append(unlocks, mu.RUnlock)
			} else {
				mu := (*sync.Mutex)(f.UnsafePointer())
				for range 100 {
					if mu.TryLock() {
						locked = true
						break
					}
					time.Sleep(time.Millisecond)
				}
				if !locked {
					for _, u := range unlocks {
						u()
					}
					return func() {}, false
				}
				taken[f.UnsafePointer()] = struct{}{}
				unlocks = append(unlocks, mu.Unlock)
			}
			continue
		}
		if !f.CanAddr() {
			// A copy of a struct — a decorator passed by value, say — cannot
			// have its mutex locked; the copy is unshared, so nothing can
			// race with reading it.
			continue
		}
		if !f.CanInterface() {
			// Unexported field: address it through its location.
			f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
		}
		// A value mutex can be aliased by a pointer field elsewhere in the
		// same struct; record its address so the pointer path skips it.
		if _, dup := taken[f.Addr().UnsafePointer()]; dup {
			continue
		}
		// Locked through its concrete type, like the pointer path above:
		// a lock method invoked by reflection runs outside the race
		// detector's mutex bookkeeping. An RWMutex value guards its fields
		// against writers: read-locked like its pointer counterpart, so a
		// caller's own read lock does not read as contention.
		var locked bool
		if t == rwMutexType {
			mu := (*sync.RWMutex)(f.Addr().UnsafePointer())
			for range 100 {
				if mu.TryRLock() {
					locked = true
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !locked {
				for _, u := range unlocks {
					u()
				}
				return func() {}, false
			}
			taken[f.Addr().UnsafePointer()] = struct{}{}
			unlocks = append(unlocks, mu.RUnlock)
		} else {
			mu := (*sync.Mutex)(f.Addr().UnsafePointer())
			for range 100 {
				if mu.TryLock() {
					locked = true
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !locked {
				for _, u := range unlocks {
					u()
				}
				return func() {}, false
			}
			taken[f.Addr().UnsafePointer()] = struct{}{}
			unlocks = append(unlocks, mu.Unlock)
		}
	}
	return func() {
		for _, u := range unlocks {
			u()
		}
	}, true
}
