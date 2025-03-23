package typechecker

import (
	"github.com/dave/dst"
	"go/types"
)

type Method struct {
	Receiver Struct
	Function Function
}

func (p *Package) Method(typeName, methodName string) (Method, bool) {
	// Lookup the type in the package scope
	s, ok := p.Struct(typeName)
	if !ok {
		return Method{}, false
	}

	// Find the method in the method set
	for i := 0; i < s.Type.NumMethods(); i++ {
		method := s.Type.Method(i)
		if method.Name() == methodName {
			// Find the corresponding dst.FuncDecl node
			node, ok := p.findMethodNode(typeName, methodName)
			if !ok {
				return Method{}, false
			}
			return Method{
				Receiver: s,
				Function: Function{
					Type: method,
					Node: node,
				},
			}, true
		}
	}
	return Method{}, false
}

func (p *Package) Methods(typeName string, publicOnly, includeEmbedded bool) map[string]Method {
	recv, ok := p.Struct(typeName)
	if !ok {
		return nil
	}

	methodsByName := make(map[string]Method)

	// Get inherited methods from embedded types
	if includeEmbedded {
		mset := types.NewMethodSet(recv.Type)
		for i := 0; i < mset.Len(); i++ {
			method := mset.At(i).Obj().(*types.Func)
			if publicOnly && !IsPublic(method.Name()) {
				continue
			}
			// this could be defined in a different package, so in that case we won't find the node
			node, _ := p.findMethodNode(typeName, method.Name())

			methodsByName[method.Name()] = Method{
				Receiver: recv,
				Function: Function{
					Type: method,
					Node: node,
				},
			}

		}
	}

	// Get methods declared on *types.Named
	for i := 0; i < recv.Type.NumMethods(); i++ {
		method := recv.Type.Method(i)
		if publicOnly && !IsPublic(method.Name()) {
			continue
		}
		node, ok := p.findMethodNode(typeName, method.Name())
		if !ok && !p.external {
			continue
		}
		methodsByName[method.Name()] = Method{
			Receiver: recv,
			Function: Function{
				Type: method,
				Node: node,
			},
		}
	}

	return methodsByName
}

func (p *Package) findMethodNode(typeName, methodName string) (*dst.FuncDecl, bool) {
	for _, file := range p.Files() { // Assuming `p.dec.Files` is a slice of *dst.File
		for _, decl := range file.Decls {
			fn, ok := decl.(*dst.FuncDecl)
			if !ok {
				continue
			}
			if fn.Recv == nil {
				continue
			}
			t := p.TypeOf(fn.Recv.List[0].Type)
			_, n := ExtractPackageAndName(t)

			if fn.Name.Name == methodName && n == typeName {
				return fn, true
			}
		}
	}
	return nil, false
}
