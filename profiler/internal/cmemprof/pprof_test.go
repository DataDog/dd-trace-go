package cmemprof

import (
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
)

// There are a few special cases that the Go /proc/self/maps parsing code
// handles that we want to preserve:
//
//	* If a file is deleted after the progam starts, there will be a
//	  " (deleted)" suffix which should be removed
//	* Per "man 5 proc", the file name will be unescaped except for
//	  newlines. In particular, this means the filename can contain
//	  whitespace.
//
// The following /proc/self/maps example was obtained by compiling a C program
// which just calls "sleep(1000)", giving it a name with a space ("space daemon"),
// and deleting the executable after starting the program.

var procMaps = []byte(`55f747992000-55f747993000 r--p 00000000 08:01 788178                     /usr/bin/space daemon (deleted)
55f747993000-55f747994000 r-xp 00001000 08:01 788178                     /usr/bin/space daemon (deleted)
55f747994000-55f747995000 r--p 00002000 08:01 788178                     /usr/bin/space daemon (deleted)
55f747995000-55f747996000 r--p 00002000 08:01 788178                     /usr/bin/space daemon (deleted)
55f747996000-55f747997000 rw-p 00003000 08:01 788178                     /usr/bin/space daemon (deleted)
7fa3b3c00000-7fa3b3c22000 r--p 00000000 08:01 3463                       /usr/lib/x86_64-linux-gnu/libc-2.31.so
7fa3b3c22000-7fa3b3d9a000 r-xp 00022000 08:01 3463                       /usr/lib/x86_64-linux-gnu/libc-2.31.so
7fa3b3d9a000-7fa3b3de8000 r--p 0019a000 08:01 3463                       /usr/lib/x86_64-linux-gnu/libc-2.31.so
7fa3b3de8000-7fa3b3dec000 r--p 001e7000 08:01 3463                       /usr/lib/x86_64-linux-gnu/libc-2.31.so
7fa3b3dec000-7fa3b3dee000 rw-p 001eb000 08:01 3463                       /usr/lib/x86_64-linux-gnu/libc-2.31.so
7fa3b3dee000-7fa3b3df4000 rw-p 00000000 00:00 0 
7fa3b3dfc000-7fa3b3dfd000 r--p 00000000 08:01 3459                       /usr/lib/x86_64-linux-gnu/ld-2.31.so
7fa3b3dfd000-7fa3b3e20000 r-xp 00001000 08:01 3459                       /usr/lib/x86_64-linux-gnu/ld-2.31.so
7fa3b3e20000-7fa3b3e28000 r--p 00024000 08:01 3459                       /usr/lib/x86_64-linux-gnu/ld-2.31.so
7fa3b3e29000-7fa3b3e2a000 r--p 0002c000 08:01 3459                       /usr/lib/x86_64-linux-gnu/ld-2.31.so
7fa3b3e2a000-7fa3b3e2b000 rw-p 0002d000 08:01 3459                       /usr/lib/x86_64-linux-gnu/ld-2.31.so
7fa3b3e2b000-7fa3b3e2c000 rw-p 00000000 00:00 0 
7fff1e028000-7fff1e049000 rw-p 00000000 00:00 0                          [stack]
7fff1e04c000-7fff1e04f000 r--p 00000000 00:00 0                          [vvar]
7fff1e04f000-7fff1e050000 r-xp 00000000 00:00 0                          [vdso]
ffffffffff600000-ffffffffff601000 --xp 00000000 00:00 0                  [vsyscall]
`)

func Test_parseMapping(t *testing.T) {
	want := []*profile.Mapping{
		{ID: 1, Start: 0x55f747993000, Limit: 0x55f747994000, Offset: 0x00001000, File: "/usr/bin/space daemon"},
		{ID: 2, Start: 0x7fa3b3c22000, Limit: 0x7fa3b3d9a000, Offset: 0x00022000, File: "/usr/lib/x86_64-linux-gnu/libc-2.31.so"},
		{ID: 3, Start: 0x7fa3b3dfd000, Limit: 0x7fa3b3e20000, Offset: 0x00001000, File: "/usr/lib/x86_64-linux-gnu/ld-2.31.so"},
		{ID: 4, Start: 0x7fff1e04f000, Limit: 0x7fff1e050000, Offset: 0x00000000, File: "[vdso]"},
		{ID: 5, Start: 0xffffffffff600000, Limit: 0xffffffffff601000, Offset: 0x00000000, File: "[vsyscall]"},
	}
	got := parseMappings(procMaps)
	require.Equal(t, want, got)
}
