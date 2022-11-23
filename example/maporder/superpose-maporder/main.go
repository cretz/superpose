package main

import (
	"fmt"
	"go/ast"
	"go/types"

	"github.com/cretz/superpose"
	"golang.org/x/tools/go/packages"
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

func (*transformer) AppliesToPackage(ctx *superpose.TransformContext, pkg *packages.Package) (bool, error) {
	// Does not apply to "runtime" or our impl
	return pkg.PkgPath != "runtime" && pkg.PkgPath != mapIterPkg, nil
}

func (t *transformer) Transform(
	ctx *superpose.TransformContext,
	pkgs []*superpose.TransformPackage,
) ([]*superpose.TransformedPackage, error) {
	if !t.sorted {
		return nil, fmt.Errorf("insertion not supported yet")
	}
	// Transform every non-cached-but-transformed package
	var transforms []*superpose.TransformedPackage
	for _, pkg := range pkgs {
		if !pkg.Cached && pkg.Transformed {
			if patches, err := transformPackage(pkg.Package); err != nil {
				return nil, fmt.Errorf("package %v failed transform: %w", pkg, err)
			} else if len(patches) > 0 {
				transforms = append(transforms, &superpose.TransformedPackage{ID: pkg.ID, Patches: patches})
			}
		}
	}
	return transforms, nil
}

func transformPackage(pkg *packages.Package) ([]*superpose.Patch, error) {
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

func transformNode(pkg *packages.Package, node ast.Node) *superpose.Patch {
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
