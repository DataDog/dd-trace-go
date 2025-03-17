package codegen

import (
	"github.com/DataDog/dd-trace-go/internal/tools/process_contribs/internal/typing"
	"github.com/dave/dst"
	"slices"
)

func AddStructField(structType *dst.StructType, field *dst.Field) {
	structType.Fields.List = append(structType.Fields.List, field)
}

func RemoveStructInjectedFields(structType *dst.StructType, fields []string) {
	var keep []*dst.Field

	if structType.Fields == nil {
		return
	}
	for _, f := range structType.Fields.List {
		if slices.Contains(fields, typing.GetFieldName(f)) && typing.IsInjectedField(f) {
			continue
		}
		keep = append(keep, f)
	}
	structType.Fields.List = keep
}
