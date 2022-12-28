package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/cretz/superpose"
	// We include the clock because we want to force it to be compiled ahead of
	// time
	_ "github.com/cretz/superpose/example/mocktime/clock"
)

func main() {
	superpose.RunMain(
		context.Background(),
		superpose.Config{
			Version:      superpose.MustLoadCurrentExeContentID(),
			Transformers: map[string]superpose.Transformer{"mocktime": transformer{}},
			// Set to true to see compilation details
			Verbose: false,
		},
		superpose.RunMainConfig{
			AssumeToolexec: true,
		},
	)
}

type transformer struct{}

func (transformer) AppliesToPackage(ctx *superpose.TransformContext, pkgPath string) (bool, error) {
	// For now, the only stdlib packages we'll apply to are log and time
	if pkgPath == "log" || pkgPath == "time" {
		return true, nil
	}
	// Also any of our packages but the clock itself which we want shared
	return strings.HasPrefix(pkgPath, "github.com/cretz/superpose/example/mocktime") &&
		!strings.Contains(pkgPath, "clock"), nil
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
	if pkg.PkgPath != "time" {
		return res, nil
	}

	// When we encounter the time.Now() function, we want to replace its entire
	// body with our mocked form. We also need to import our clock, so we do that
	// after the last import. We take care not to mess up original line numbers.
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
			if funcObj == nil || funcObj.FullName() != "time.Now" {
				continue
			}

			// Add our custom string import just after the last import, but on the
			// same line to prevent inadvertently altering line numbers.
			res.Patches = append(res.Patches, &superpose.Patch{
				Range: superpose.Range{Pos: lastImportEndPos},
				Str:   `; import __clock "github.com/cretz/superpose/example/mocktime/clock"`,
			})

			// We have to also tell the linker that we have a new dependency
			res.IncludeDependentPackages = map[string]struct{}{"github.com/cretz/superpose/example/mocktime/clock": {}}

			// Now we want to replace the body with our return, but also we need a
			// line directive to tell it to pick up where it left off when it sees the
			// rbrace
			rbracePos := pkg.Fset.Position(decl.Body.Rbrace)
			res.Patches = append(res.Patches, &superpose.Patch{
				Range: superpose.Range{Pos: decl.Body.Lbrace + 1, End: decl.Body.Rbrace - 1},
				Str:   " return UnixMilli(__clock.NowUnixMilli); /*line :" + strconv.Itoa(rbracePos.Line) + "*/",
			})

			return res, nil
		}
	}
	return nil, fmt.Errorf("could not find Logger.Output")
}
