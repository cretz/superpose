package main

import (
	"context"
	"go/ast"
	"go/types"
	"strings"

	"github.com/cretz/superpose"
)

func main() {
	superpose.RunMain(
		context.Background(),
		superpose.Config{
			Version:      superpose.MustLoadCurrentExeContentID(),
			Transformers: map[string]superpose.Transformer{"tests-simple": transformer{}},
			Verbose:      true,
		},
		superpose.RunMainConfig{
			AssumeToolexec: true,
		},
	)
}

type transformer struct{}

func (transformer) AppliesToPackage(ctx *superpose.TransformContext, pkgPath string) (bool, error) {
	return strings.HasPrefix(pkgPath, "github.com/cretz/superpose/tests/simple"), nil
}

func (transformer) Transform(
	ctx *superpose.TransformContext,
	pkg *superpose.TransformPackage,
) (*superpose.TransformResult, error) {
	// Change any ReturnString function to return "foo"
	res := &superpose.TransformResult{
		AddLineDirectives: true,
		LogPatchedFiles:   true,
	}
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			// Add patch if it's the func we want
			decl, _ := decl.(*ast.FuncDecl)
			if decl == nil {
				continue
			}
			funcObj, _ := pkg.TypesInfo.ObjectOf(decl.Name).(*types.Func)
			if funcObj == nil || funcObj.FullName() != "github.com/cretz/superpose/tests/simple.ReturnString" {
				continue
			}
			res.Patches = append(res.Patches, &superpose.Patch{
				Range: superpose.Range{Pos: decl.Body.Lbrace + 1, End: decl.Body.Rbrace},
				Str:   ` return "foo" `,
			})
		}
	}
	return res, nil
}
