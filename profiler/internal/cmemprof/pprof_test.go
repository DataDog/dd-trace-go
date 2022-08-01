package cmemprof

import (
	"bytes"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
)

func Test_parseMapping(t *testing.T) {
	implementations := []struct {
		Name string
		Func func([]byte) []*profile.Mapping
	}{
		{"parseMappingsOriginal", parseMappingsOriginal},
		{"parseMappings", parseMappings},
	}
	example := strings.TrimSpace(`
address           perms offset  dev   inode       pathname
00400000-00452000 r-xp 00000000 08:02 173521      /usr/bin/dbus-daemon
00300000-00352000 r-xp 00000000 08:02 173521      /usr/bin/white space/daemon
`) + "\n"

	for _, impl := range implementations {
		t.Run(impl.Name, func(t *testing.T) {
			got := impl.Func([]byte(example))
			require.Equal(t, []*profile.Mapping{
				{
					ID:              2,
					Start:           3145728,
					Limit:           3481600,
					Offset:          0,
					File:            "/usr/bin/white space/daemon",
					BuildID:         "",
					HasFunctions:    false,
					HasFilenames:    false,
					HasLineNumbers:  false,
					HasInlineFrames: false,
				},
				{
					ID:              1,
					Start:           4194304,
					Limit:           4530176,
					Offset:          0,
					File:            "/usr/bin/dbus-daemon",
					BuildID:         "",
					HasFunctions:    false,
					HasFilenames:    false,
					HasLineNumbers:  false,
					HasInlineFrames: false,
				},
			}, got)
		})
	}
}

// bytes.Cut, but backported so we can still support Go 1.17
func bytesCut(s, sep []byte) (before, after []byte, found bool) {
	if i := bytes.Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, nil, false
}

func stringsCut(s, sep string) (before, after string, found bool) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

func parseMappingsOriginal(data []byte) []*profile.Mapping {
	var results []*profile.Mapping
	var line []byte
	// next removes and returns the next field in the line.
	// It also removes from line any spaces following the field.
	next := func() []byte {
		var f []byte
		f, line, _ = bytesCut(line, []byte(" "))
		line = bytes.TrimLeft(line, " ")
		return f
	}

	for len(data) > 0 {
		line, data, _ = bytesCut(data, []byte("\n"))
		addr := next()
		loStr, hiStr, ok := stringsCut(string(addr), "-")
		if !ok {
			continue
		}
		lo, err := strconv.ParseUint(loStr, 16, 64)
		if err != nil {
			continue
		}
		hi, err := strconv.ParseUint(hiStr, 16, 64)
		if err != nil {
			continue
		}
		perm := next()
		if len(perm) < 4 || perm[2] != 'x' {
			// Only interested in executable mappings.
			continue
		}
		offset, err := strconv.ParseUint(string(next()), 16, 64)
		if err != nil {
			continue
		}
		next()          // dev
		inode := next() // inode
		if line == nil {
			continue
		}
		file := string(line)

		// Trim deleted file marker.
		deletedStr := " (deleted)"
		deletedLen := len(deletedStr)
		if len(file) >= deletedLen && file[len(file)-deletedLen:] == deletedStr {
			file = file[:len(file)-deletedLen]
		}

		if len(inode) == 1 && inode[0] == '0' && file == "" {
			// Huge-page text mappings list the initial fragment of
			// mapped but unpopulated memory as being inode 0.
			// Don't report that part.
			// But [vdso] and [vsyscall] are inode 0, so let non-empty file names through.
			continue
		}

		results = append(results, &profile.Mapping{
			ID:     uint64(len(results) + 1),
			Start:  lo,
			Limit:  hi,
			Offset: offset,
			File:   file,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Start < results[j].Start
	})

	return results
}
