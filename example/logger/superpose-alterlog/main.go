package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"github.com/cretz/superpose"
)

func main() {
	superpose.RunMain(
		context.Background(),
		superpose.Config{
			Version:      "1",
			Transformers: map[string]superpose.Transformer{"alterlog": transformer{}},
			// Set to true to see compilation details
			Verbose: false,
			// We'll disable the cache for demo purposes, but users should usually
			// never set this
			ForceTransform: true,
		},
		superpose.RunMainConfig{
			AssumeToolexec: true,
		},
	)
}

type transformer struct{}

func (transformer) AppliesToPackage(ctx *superpose.TransformContext, pkgPath string) (bool, error) {
	// Our dimension applies to the standard logging package and our sample
	// package
	return pkgPath == "log" || pkgPath == "github.com/cretz/superpose/example/logger", nil
}

func (transformer) Transform(
	ctx *superpose.TransformContext,
	pkg *superpose.TransformPackage,
) (*superpose.TransformResult, error) {
	ctx.Superpose.Debugf("Transforming package %v", pkg.PkgPath)
	// We only want to transform the log package
	res := &superpose.TransformResult{
		AddLineDirectives: true,
		LogPatchedFiles:   true,
	}
	if pkg.PkgPath != "log" {
		return res, nil
	}

	// Specifically we want to transform Logger.Output to always replace "Hello"
	// with "Aloha". So we must first find that method decl, and then we will put
	// our replacement on the same line as the opening brace to keep all other
	// line numbers intact.
	for _, file := range pkg.Syntax {
		var lastImportEndPos token.Pos
		for _, decl := range file.Decls {
			// Track last import
			if decl, _ := decl.(*ast.GenDecl); decl != nil && decl.Tok == token.IMPORT {
				lastImportEndPos = decl.End()
			}

			// Make sure it's the func we want
			decl, _ := decl.(*ast.FuncDecl)
			if decl == nil {
				continue
			}
			funcObj, _ := pkg.TypesInfo.ObjectOf(decl.Name).(*types.Func)
			if funcObj == nil || funcObj.FullName() != "(*log.Logger).Output" {
				continue
			}

			// Add our custom string import just after the last import, but on the
			// same line to prevent inadvertently altering line numbers.
			res.Patches = append(res.Patches, &superpose.Patch{
				Range: superpose.Range{Pos: lastImportEndPos},
				Str:   `; import __strings "strings"`,
			})

			// We have to also tell the linker that we have a new dependency on
			// "strings" just in case it wasn't there before
			res.IncludeDependentPackages = map[string]struct{}{"strings": {}}

			// Now change the second parameter, string, to replace "Hello" with
			// "Aloha". Note, we don't assume the param name, we obtain it for
			// correctness.
			paramName := funcObj.Type().(*types.Signature).Params().At(1).Name()
			res.Patches = append(res.Patches, &superpose.Patch{
				Range: superpose.Range{Pos: decl.Body.Lbrace + 1},
				Str:   fmt.Sprintf(`%[1]v = __strings.ReplaceAll(%[1]v, "Hello", "Aloha")`, paramName),
			})

			return res, nil
		}
	}
	return nil, fmt.Errorf("could not find Logger.Output")
}
