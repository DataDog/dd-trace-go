package typing

import (
	"github.com/dave/dst"
	"slices"
)

func GetFieldName(f *dst.Field) string {
	if len(f.Names) == 0 {
		return ""
	}
	return f.Names[0].Name
}

func IsInjectedField(f *dst.Field) bool {
	decs := f.Decorations()
	return slices.Contains(decs.Start.All(), "//ddtrace:gen-entrypoint:start")
}
