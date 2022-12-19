package main

import (
	"fmt"
	"go/ast"
	"go/types"

	"github.com/cretz/superpose"
)

func main() {
	superpose.RunMain(
		superpose.Config{
			Version: "1",
			Transformers: map[string]superpose.Transformer{
				// Transform both of these dimensions
				"maporder_sorted": &transformer{sorted: true},
				// "mapsort_insertion": &transformer{sorted: false},
			},
			Verbose: true,
		},
		superpose.RunMainConfig{
			AssumeToolexec: true,
		},
	)
}

type transformer struct {
	sorted bool
}

const (
	mapIterPkg   = "github.com/cretz/superpose/example/maporder/superpose-maporder/mapiter"
	mapIterAlias = "__mapiter"
)

func (*transformer) AppliesToPackage(ctx *superpose.TransformContext, pkgPath string) (bool, error) {
	// TODO: Remove this part where it's only applying to our specific piece
	// during test
	return pkgPath == "github.com/cretz/superpose/example/maporder" ||
		pkgPath == "github.com/cretz/superpose/example/maporder/otherpkg", nil
	// // Does not apply to "runtime" or our impl
	// return pkg.PkgPath != "runtime" && pkg.PkgPath != mapIterPkg, nil
}

func (t *transformer) Transform(
	ctx *superpose.TransformContext,
	pkg *superpose.TransformPackage,
) ([]*superpose.Patch, error) {
	ctx.Superpose.Debugf("Transforming package %v", pkg.PkgPath)
	// Go over each file adding patches if there are any
	var patches []*superpose.Patch
	for _, file := range pkg.Syntax {
		patchedFile := false
		ast.Inspect(file, func(n ast.Node) bool {
			if nodePatch := transformNode(pkg, n); nodePatch != nil {
				patches = append(patches, nodePatch)
				patchedFile = true
			}
			return true
		})
		if patchedFile {
			// We add our import at the very top on the same line as package
			patches = append(patches, &superpose.Patch{
				Range: superpose.Range{Pos: file.Name.End()},
				Str:   fmt.Sprintf("; import %s %q", mapIterAlias, mapIterPkg),
			})
		}
	}
	return patches, nil
}

func transformNode(pkg *superpose.TransformPackage, node ast.Node) *superpose.Patch {
	rangeStmt, _ := node.(*ast.RangeStmt)
	if rangeStmt == nil {
		return nil
	}
	rangeType, _ := pkg.TypesInfo.TypeOf(rangeStmt.X).(*types.Map)
	if rangeType == nil {
		return nil
	}

	// If the map has an unordered key, just change the range statement to panic
	if b, _ := rangeType.Key().(*types.Basic); b == nil || b.Info()&types.IsOrdered == 0 {
		return superpose.WrapWithPatch(rangeStmt.X, mapIterAlias+".PanicUnorderedKeys(", ")")
	}

	// Change to:
	//   for __iter := __mapiter.NewSortedIter(<X>); __iter.Next(); { <Key>, <Val> :=|= __iter.Pair()
	patch := &superpose.Patch{
		Range: superpose.Range{Pos: rangeStmt.Pos(), End: rangeStmt.Body.Lbrace + 1},
		Captures: map[string]superpose.Range{
			"x": superpose.RangeOf(rangeStmt.X),
		},
		Str: "for __iter := " + mapIterAlias + ".NewSortedIter({{.x}}); __iter.Next(); {",
	}
	if rangeStmt.Key != nil || rangeStmt.Value != nil {
		if rangeStmt.Key != nil {
			patch.Captures["key"] = superpose.RangeOf(rangeStmt.Key)
			patch.Str += " {{.key}}, "
		} else {
			patch.Str += " _, "
		}
		if rangeStmt.Value != nil {
			patch.Captures["value"] = superpose.RangeOf(rangeStmt.Value)
			patch.Str += "{{.value}} "
		} else {
			patch.Str += "_ "
		}
		patch.Str += rangeStmt.Tok.String() + " __iter.Pair()"
	}
	return patch
}
