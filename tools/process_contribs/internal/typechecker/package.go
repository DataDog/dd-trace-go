package typechecker

import (
	"errors"
	"fmt"
	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/comment"
	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/dave/dst/dstutil"
	"github.com/hashicorp/go-multierror"
	"go/ast"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/packages"
	"strconv"
	"strings"
)

type Package struct {
	pkg      *decorator.Package
	changes  bool
	external bool
}

// LoadPackage loads a package from a fs path.
func LoadPackage(dir string) (Package, error) {
	pkgs, err := decorator.Load(&packages.Config{Dir: dir, Mode: packages.LoadSyntax})
	if err != nil {
		return Package{}, err
	}
	if len(pkgs) == 0 || len(pkgs) > 1 {
		return Package{}, fmt.Errorf("expected exactly 1 package, got: %d", len(pkgs))
	}
	pkg := pkgs[0]
	return Package{pkg: pkg}, nil
}

// LoadExternalPackage loads an external package from the given url.
func LoadExternalPackage(pkgUrl string) (Package, error) {
	pkgs, err := decorator.Load(&packages.Config{Mode: packages.LoadSyntax}, pkgUrl)
	if err != nil {
		return Package{}, err
	}
	if len(pkgs) == 0 || len(pkgs) > 1 {
		return Package{}, fmt.Errorf("expected exactly 1 package, got: %d", len(pkgs))
	}
	pkg := pkgs[0]
	return Package{pkg: pkg, external: true}, nil
}

func (p *Package) Name() string {
	return p.pkg.Name
}

func (p *Package) Path() string {
	return p.pkg.PkgPath
}

func (p *Package) Files() []*dst.File {
	return p.pkg.Syntax
}

func (p *Package) Filename(f *dst.File) string {
	return p.pkg.Decorator.Filenames[f]
}

func (p *Package) TypeOf(expr dst.Expr) types.Type {
	astExpr := p.pkg.Decorator.Ast.Nodes[expr].(ast.Expr)
	return p.pkg.TypesInfo.TypeOf(astExpr)
}

func (p *Package) Validate() error {
	name := p.Name()
	if strings.HasSuffix(name, "_test") || name == "main" {
		return nil
	}

	err := &multierror.Error{}

	var allConstsAndVars []*dst.GenDecl
	var allPublicFuncs []*dst.FuncDecl
	for _, f := range p.Files() {
		fName := p.pkg.Decorator.Filenames[f]
		if strings.HasSuffix(fName, "_test.go") || strings.HasSuffix(fName, "_example.go") {
			continue
		}
		for _, decl := range f.Decls {
			switch t := decl.(type) {
			case *dst.FuncDecl:
				shouldSkip := !IsPublic(t.Name.Name) || // ignore private functions
					t.Recv != nil || // ignore methods
					p.isFunctionalOption(t) // ignore functional options

				if shouldSkip {
					continue
				}
				allPublicFuncs = append(allPublicFuncs, t)

			case *dst.GenDecl:
				if t.Tok == token.CONST || t.Tok == token.VAR {
					allConstsAndVars = append(allConstsAndVars, t)
				}
			}
		}
	}

	foundInstr := false
	for _, c := range allConstsAndVars {
		if isTargetConstOrVar(c, "instr") {
			foundInstr = true
			break
		}
	}
	if !foundInstr {
		err = multierror.Append(err, errors.New("\"instr\" package-level declaration not found"))
	}

	for _, fn := range allPublicFuncs {
		foundEntrypointComment := false
		comments := fn.Decorations().Start
		for _, c := range comments {
			_, ok := comment.ParseComment(c)
			if ok {
				if foundEntrypointComment {
					err = multierror.Append(err, fmt.Errorf("public function %s has multiple entrypoint comments", fn.Name.Name))
					break
				} else {
					foundEntrypointComment = true
				}
			}
		}
		if !foundEntrypointComment {
			err = multierror.Append(err, fmt.Errorf("public function %s does not have entrypoint comment", fn.Name.Name))
		}
	}

	return err.ErrorOrNil()
}

func (p *Package) FunctionAvailableIdent(fn *dst.FuncDecl, identPrefix string) string {
	tryName := identPrefix
	count := 1
	for p.isFuncIdentifierUsed(fn, tryName) {
		tryName = identPrefix + strconv.Itoa(count)
		count++
	}
	return tryName
}

func (p *Package) isFuncIdentifierUsed(fn *dst.FuncDecl, targetIdent string) bool {
	var f Function
	if fn.Recv != nil {
		recvType := p.TypeOf(fn.Recv.List[0].Type)
		_, name := ExtractPackageAndName(recvType)
		m, _ := p.Method(name, fn.Name.Name)
		f = m.Function
	} else {
		f, _ = p.Function(fn.Name.Name)
	}

	s := f.Type.Signature()

	for arg := range s.Params().Variables() {
		if arg.Name() == targetIdent {
			return true
		}
	}
	for ret := range s.Results().Variables() {
		if ret.Name() == targetIdent {
			return true
		}
	}
	for _, stmt := range fn.Body.List {
		if assign, ok := stmt.(*dst.AssignStmt); ok {
			for _, l := range assign.Lhs {
				if ident, ok := l.(*dst.Ident); ok {
					if ident.Name == targetIdent {
						return true
					}
				}
			}
		}
	}
	return false
}

func (p *Package) isFunctionalOption(fn *dst.FuncDecl) bool {
	f, ok := p.Function(fn.Name.Name)
	if !ok {
		panic("function not found")
	}
	s := f.Type.Signature()

	// functional options return exactly 1 result
	if s.Results().Len() != 1 {
		return false
	}
	ret := s.Results().At(0)
	retType := ret.Type().String()
	return strings.Contains(strings.ToLower(retType), "option")
}

type ApplyFunc func(cur *dstutil.Cursor) (changed bool, cont bool)

func (u ApplyFunc) Chain(f ApplyFunc) ApplyFunc {
	if u == nil {
		return f
	}
	return func(cur *dstutil.Cursor) (bool, bool) {
		ch1, cont1 := u(cur)
		ch2, cont2 := f(cur)
		return ch1 || ch2, cont1 || cont2
	}
}

type MultiApplyFunc []ApplyFunc

func (u MultiApplyFunc) Merge() ApplyFunc {
	if len(u) == 0 {
		return nil
	}
	r := u[0]
	for _, f := range u[1:] {
		r = r.Chain(f)
	}
	return r
}

func (p *Package) ApplyChanges(fns ...ApplyFunc) {
	if len(fns) == 0 {
		return
	}
	applyFn := fns[0]
	for _, fn := range fns[1:] {
		applyFn = applyFn.Chain(fn)
	}

	for _, f := range p.Files() {
		dstutil.Apply(f, func(cur *dstutil.Cursor) bool {
			changed, cont := applyFn(cur)
			if changed {
				p.changes = true
			}
			return cont
		}, nil)
	}
}

func (p *Package) Save() error {
	if !p.changes {
		return nil
	}
	return p.pkg.Save()
}
