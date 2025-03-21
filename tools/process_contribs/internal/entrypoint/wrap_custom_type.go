package entrypoint

import (
	"fmt"
	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/dsthelpers"
	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/typechecker"
	"github.com/dave/dst"
	"github.com/dave/dst/dstutil"
	"slices"
	"strings"
)

type entrypointWrapCustomType struct{}

// Apply does the following:
//
// 1. add a new field `disabled bool` in the custom type
// 2. get the value of disabled and store it in the struct
// 3. loop over the methods that match from the original argument and the return one
// 4. in all those methods, add the if check and call the original without doing anything if disabled = true
func (e entrypointWrapCustomType) Apply(fn typechecker.Function, pkg typechecker.Package, args map[string]string) (typechecker.ApplyFunc, error) {
	skipMethods := strings.Split(args["skip-methods"], ",")
	s := fn.Type.Signature()

	incompatibleSignature :=
		s.Results().Len() == 0 ||
			s.Results().Len() > 2 ||
			(s.Results().Len() == 2 && s.Results().At(1).Type().String() != "error")

	if incompatibleSignature {
		return nil, fmt.Errorf("incompatible function signature (only know how to handle single result or result/error): %s", s.Results().String())
	}

	customType, ok := pkg.Struct(s.Results().At(0).Type().String())
	if !ok {
		return nil, fmt.Errorf("struct type definition not found: %s", customType)
	}

	// check if a field disabled already exists, without the inject comments
	for i := 0; i < customType.DefinitionType.NumFields(); i++ {
		field := customType.DefinitionType.Field(i)
		dstField := customType.DefinitionNode.Fields.List[i]

		if field.Name() == "disabled" && !dsthelpers.FieldIsInjected(dstField) {
			return nil, fmt.Errorf("type declaration %q already has a struct field named \"disabled\"", customType.Type)
		}
	}

	// Add disabled field to struct
	changes := e.addDisabledField(customType)

	// Prepend code in entrypoint function
	changes = changes.Chain(e.prependEntrypointFunctionCode(pkg, fn))

	// Prepend code in all public methods
	customMethodsChange, err := e.prependCustomMethodsCode(pkg, fn, customType, skipMethods)
	if err != nil {
		return nil, err
	}
	changes = changes.Chain(customMethodsChange)

	return changes, nil
}

func (e entrypointWrapCustomType) prependEntrypointFunctionCode(pkg typechecker.Package, fn typechecker.Function) typechecker.ApplyFunc {
	customTypeRetName := dsthelpers.FunctionSetReturnName(fn.Node, pkg, 0, "result")

	tmpl := `defer func() {
	if {{.Name}} != nil {
		{{.Name}}.disabled = disabled
	}
}()`

	assignStmt := dsthelpers.StatementAssignVariable("disabled", dsthelpers.ExpressionIntegrationDisabled())
	deferStmt := dsthelpers.StatementFromTemplate(tmpl, map[string]any{"Name": customTypeRetName})

	newLines := dsthelpers.StatementWithGenCodeDecorations(
		assignStmt,
		deferStmt,
	)

	return func(cur *dstutil.Cursor) (changed bool, cont bool) {
		if cur.Node() == fn.Node {
			dsthelpers.FunctionRemoveInjectedBlocks(fn.Node)
			fn.Node.Body.List = append([]dst.Stmt{newLines}, fn.Node.Body.List...)
			return true, false
		}
		return false, true
	}
}

func (e entrypointWrapCustomType) addDisabledField(customType typechecker.Struct) typechecker.ApplyFunc {
	newField := dsthelpers.FieldWithGenCodeDecorations("disabled", "bool")

	return func(cur *dstutil.Cursor) (changed bool, cont bool) {
		if cur.Node() == customType.Node {
			dsthelpers.StructRemoveFields(customType.Node, "disabled")
			dsthelpers.StructAddFields(customType.Node, newField)
			return true, false
		}
		return false, true
	}
}

func (e entrypointWrapCustomType) prependCustomMethodsCode(pkg typechecker.Package, entrypointFunc typechecker.Function, customType typechecker.Struct, skipMethods []string) (typechecker.ApplyFunc, error) {
	s := entrypointFunc.Type.Signature()
	customTypeName := customType.Type.Obj().Name()

	publicMethods := pkg.Methods(customTypeName, true, false)

	// the original type should be either embedded or a field in the custom type
	originalType := s.Params().At(0).Type()
	embeddedFieldName := ""
	for f := range customType.DefinitionType.Fields() {
		if f.Type().String() == originalType.String() {
			embeddedFieldName = f.Name()
			break
		}
	}
	if embeddedFieldName == "" {
		return nil, fmt.Errorf("could not find original type in any of the fields from custom type: %s", customType.Type.String())
	}

	originalPkgPath, originalTypeName := typechecker.ExtractPackageAndName(originalType)

	originalTypePkg, err := typechecker.LoadExternalPackage(originalPkgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load package: %w", err)
	}

	originalTypePublicMethods := originalTypePkg.Methods(originalTypeName, true, true)

	changes := make(typechecker.MultiApplyFunc, 0)
	for name, _ := range publicMethods {
		if slices.Contains(skipMethods, name) {
			continue
		}
		_, ok := originalTypePublicMethods[name]
		if !ok {
			return nil, fmt.Errorf("method not found in original type %s: %s", originalType, name)
		}
		m, ok := pkg.Method(customTypeName, name)
		if !ok {
			return nil, fmt.Errorf("method not found: %s", name)
		}
		changes = append(changes, e.disabledMethod(pkg, m, embeddedFieldName))
	}

	return changes.Merge(), nil
}

func (e entrypointWrapCustomType) disabledMethod(pkg typechecker.Package, method typechecker.Method, fieldName string) typechecker.ApplyFunc {
	recvName := dsthelpers.FunctionSetReceiverName(method.Function.Node, pkg.FunctionAvailableIdent(method.Function.Node, "r"))

	var retLines []string
	if method.Function.Type.Signature().Results().Len() > 0 {
		retLines = append(retLines, "return {{.Receiver}}.{{.Field}}.{{.MethodName}}({{.MethodArgs}})")
	} else {
		retLines = append(retLines, "{{.Receiver}}.{{.Field}}.{{.MethodName}}({{.MethodArgs}})", "return")
	}

	tpl := fmt.Sprintf(`if {{.Receiver}}.disabled {
	%s
}`, strings.Join(retLines, "\n\t"))

	var rets []string
	for arg := range method.Function.Type.Signature().Params().Variables() {
		rets = append(rets, arg.Name())
	}
	stmt := dsthelpers.StatementFromTemplate(tpl, map[string]any{
		"Receiver":   recvName,
		"Field":      fieldName,
		"MethodName": method.Function.Node.Name.Name,
		"MethodArgs": strings.Join(rets, ", "),
	})
	newLines := dsthelpers.StatementWithDecorations(stmt)

	return func(cur *dstutil.Cursor) (changed bool, cont bool) {
		if cur.Node() != method.Function.Node {
			return false, true
		}
		dsthelpers.FunctionRemoveInjectedBlocks(method.Function.Node)
		method.Function.Node.Body.List = append([]dst.Stmt{newLines}, method.Function.Node.Body.List...)
		return true, false
	}
}
