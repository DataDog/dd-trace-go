package typechecker

import (
	"github.com/dave/dst"
	"go/token"
	"go/types"
	"strings"
	"unicode"
)

// ExtractPackageAndName is used to extract the package and nameof a type with the following syntax: *<Path>.<Name>
// Examples:
//   - *my/package/path.SomeType -> "my/package/path", "SomeType"
//   - string -> "", "string"
func ExtractPackageAndName(t types.Type) (string, string) {
	s := strings.TrimPrefix(t.String(), "*")
	lastDot := strings.LastIndex(s, ".")
	if lastDot == -1 {
		return "", s
	}
	return s[:lastDot], s[lastDot+1:]
}

func IsPublic(s string) bool {
	return unicode.IsUpper(rune(s[0]))
}

func isTargetConstOrVar(decl *dst.GenDecl, targetName string) bool {
	if decl.Tok != token.CONST && decl.Tok != token.VAR {
		return false
	}
	if len(decl.Specs) == 0 {
		return false
	}
	spec := decl.Specs[0]
	valueSpec, ok := spec.(*dst.ValueSpec)
	if !ok {
		return false
	}
	if len(valueSpec.Names) == 0 {
		return false
	}
	name := valueSpec.Names[0].Name
	return name == targetName
}
