package simple

import (
	"go/ast"
	"strings"

	"github.com/cretz/superpose"
)

const Dimension = "simple-dim"

func NewTransformer() superpose.Transformer { return transformer{} }

type transformer struct{}

func (transformer) AppliesToPackage(ctx *superpose.TransformContext, pkgPath string) (bool, error) {
	return strings.HasPrefix(pkgPath, "github.com/cretz/superpose/tests/simple"), nil
}

func (transformer) Transform(
	ctx *superpose.TransformContext,
	pkg *superpose.TransformPackage,
) (*superpose.TransformResult, error) {
	// We're gonna just change ReturnString to return something else
	res := &superpose.TransformResult{}
	if pkg.PkgPath != "github.com/cretz/superpose/tests/simple/package1" {
		return res, nil
	}
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			decl, _ := decl.(*ast.FuncDecl)
			if decl == nil || decl.Name.Name != "ReturnString" {
				continue
			}
			res.Patches = append(res.Patches, &superpose.Patch{
				Range: superpose.RangeOf(decl.Body),
				Str:   `{ return "bar" }`,
			})
		}
	}
	return res, nil
}
