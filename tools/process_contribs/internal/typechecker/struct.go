package typechecker

import (
	"github.com/dave/dst"
	"go/types"
	"strings"
)

type Struct struct {
	Type           *types.Named
	Node           *dst.TypeSpec
	DefinitionType *types.Struct
	DefinitionNode *dst.StructType
}

func (p *Package) Struct(name string) (Struct, bool) {
	// get just the type name
	parts := strings.Split(name, ".")
	name = parts[len(parts)-1]

	obj := p.pkg.Types.Scope().Lookup(name)
	if obj == nil {
		return Struct{}, false
	}
	// Ensure it's a TypeName (which represents a named type)
	typeName, ok := obj.(*types.TypeName)
	if !ok {
		return Struct{}, false
	}
	// Extract the underlying struct type
	named, ok := typeName.Type().(*types.Named)
	if !ok {
		return Struct{}, false
	}
	// verify it is a struct
	def, ok := named.Underlying().(*types.Struct)
	if !ok {
		return Struct{}, false
	}
	node, ok := p.findStructNode(name)
	if !ok && !p.external {
		return Struct{}, false
	}
	var defNode *dst.StructType
	if node != nil {
		defNode = node.Type.(*dst.StructType)
	}
	return Struct{
		Type:           named,
		Node:           node,
		DefinitionType: def,
		DefinitionNode: defNode,
	}, true
}

func (p *Package) findStructNode(name string) (*dst.TypeSpec, bool) {
	for _, file := range p.Files() { // Assuming `p.dec.Files` is a slice of *dst.File
		for _, decl := range file.Decls {
			d, ok := decl.(*dst.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range d.Specs {
				if typeSpec, ok := spec.(*dst.TypeSpec); ok && typeSpec.Name.Name == name {
					return typeSpec, true
				}
			}
		}
	}
	return nil, false
}
