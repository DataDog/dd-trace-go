package typechecker

import (
	"github.com/dave/dst"
	"go/types"
)

type Function struct {
	Type *types.Func
	Node *dst.FuncDecl
}

func (p *Package) FunctionSignature(ft *dst.FuncType) *types.Signature {
	return p.TypeOf(ft).(*types.Signature)
}

func (p *Package) Function(name string) (Function, bool) {
	obj := p.pkg.Types.Scope().Lookup(name)
	if obj == nil {
		return Function{}, false
	}
	fn, ok := obj.(*types.Func)
	if !ok {
		return Function{}, false
	}
	node, ok := p.findFuncNode(name)
	if !ok {
		return Function{}, false
	}
	return Function{
		Type: fn,
		Node: node,
	}, true
}

func (p *Package) Functions(publicOnly bool) map[string]Function {
	res := make(map[string]Function)
	for _, f := range p.Files() {
		for _, decl := range f.Decls {
			node, ok := decl.(*dst.FuncDecl)
			if !ok {
				continue
			}
			if node.Recv != nil {
				continue
			}
			if publicOnly && !IsPublic(node.Name.Name) {
				continue
			}
			fn, ok := p.Function(node.Name.Name)
			if !ok {
				continue
			}
			res[node.Name.Name] = fn
		}
	}
	return res
}

func (p *Package) findFuncNode(name string) (*dst.FuncDecl, bool) {
	for _, file := range p.Files() { // Assuming `p.dec.Files` is a slice of *dst.File
		for _, decl := range file.Decls {
			fn, ok := decl.(*dst.FuncDecl)
			if !ok {
				continue
			}
			if fn.Name.Name == name {
				return fn, true
			}
		}
	}
	return nil, false
}
