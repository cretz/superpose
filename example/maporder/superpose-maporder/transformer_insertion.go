package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"

	"github.com/cretz/superpose"
	"golang.org/x/tools/go/ast/inspector"
)

type transformerInsertion struct{}

func (transformerInsertion) AppliesToPackage(ctx *superpose.TransformContext, pkgPath string) (bool, error) {
	// We could make this apply across most of the standard library, but we'll
	// just keep it limited to these for now
	return pkgPath == "github.com/cretz/superpose/example/maporder" ||
		pkgPath == "github.com/cretz/superpose/example/maporder/otherpkg", nil
}

func (transformerInsertion) Transform(
	ctx *superpose.TransformContext,
	pkg *superpose.TransformPackage,
) (*superpose.TransformResult, error) {
	ctx.Superpose.Debugf("Transforming package %v", pkg.PkgPath)
	res := &superpose.TransformResult{
		AddLineDirectives: true,
		LogPatchedFiles:   true,
	}
	// Go over each file adding patches as needed. We have to walk with an
	// inspector so that we can have the stack.
	ins := inspector.New(pkg.Syntax)
	patchedFiles := map[*ast.File]struct{}{}
	t := &transformInsertionPackage{TransformContext: ctx, TransformPackage: pkg}
	ins.WithStack(nil, func(n ast.Node, push bool, stack []ast.Node) (proceed bool) {
		// We only care about push
		if push {
			if patches := t.transformNode(n, stack); len(patches) > 0 {
				res.Patches = append(res.Patches, patches...)
				patchedFiles[stack[0].(*ast.File)] = struct{}{}
			}
		}
		return true
	})
	// For all files we patched, add our mapiter import at the top
	for file := range patchedFiles {
		res.Patches = append(res.Patches, &superpose.Patch{
			Range: superpose.Range{Pos: file.Name.End()},
			Str:   fmt.Sprintf("; import %s %q", mapIterAlias, mapIterPkg),
		})
	}
	return res, nil
}

type transformInsertionPackage struct {
	*superpose.TransformContext
	*superpose.TransformPackage
	cachedFiles map[string][]byte
}

func (t *transformInsertionPackage) fileContents(file string) []byte {
	b := t.cachedFiles[file]
	if len(b) == 0 {
		if t.cachedFiles == nil {
			t.cachedFiles = map[string][]byte{}
		}
		// We intentionally just panic if we fail reading a file
		var err error
		if b, err = os.ReadFile(file); err != nil {
			panic(file)
		}
		t.cachedFiles[file] = b
	}
	return b
}

func (t *transformInsertionPackage) transformNode(n ast.Node, stack []ast.Node) []*superpose.Patch {
	// Patches needed:
	// * Map creation via "make"
	// * Map creation via literal syntax
	// * Map put
	// * Map delete
	// * Map range
	switch n := n.(type) {
	// Check if call to "make" or "delete" for a map
	case *ast.CallExpr:
		if funIdent, _ := n.Fun.(*ast.Ident); funIdent == nil || len(n.Args) == 0 {
			return nil
		} else if _, builtIn := t.TypesInfo.ObjectOf(funIdent).(*types.Builtin); !builtIn {
			// We make sure to check built-in type because anyone can create their
			// own function/var called make/delete
			return nil
		} else if funIdent.Name == "make" {
			if mapType, _ := t.TypesInfo.TypeOf(n).(*types.Map); mapType != nil {
				return t.transformMake(n, mapType)
			}
		} else if funIdent.Name == "delete" {
			if mapType, _ := t.TypesInfo.TypeOf(n.Args[0]).(*types.Map); mapType != nil {
				return t.transformDelete(n, mapType)
			}
		}
	// Check if map creation as literal
	case *ast.CompositeLit:
		if mapType, _ := t.TypesInfo.TypeOf(n).(*types.Map); mapType != nil {
			return t.transformLit(n, mapType, stack)
		}
	// Check if map put
	case *ast.AssignStmt:
		// If _any_ LHS is an index expr with X as map, it's a put of some form
		for _, x := range n.Lhs {
			if index, _ := x.(*ast.IndexExpr); index != nil {
				if _, mapType := t.TypesInfo.TypeOf(index.X).(*types.Map); mapType {
					return t.transformPut(n)
				}
			}
		}
	// Check if map range
	case *ast.RangeStmt:
		if mapType, _ := t.TypesInfo.TypeOf(n.X).(*types.Map); mapType != nil {
			return t.transformRange(n, mapType)
		}
	}
	return nil
}

func (t *transformInsertionPackage) transformMake(call *ast.CallExpr, mapType *types.Map) (patches []*superpose.Patch) {
	// It is important that we just replace the "make" part with our tracking part
	// and not mess with the potential size parameter. This allows the size
	// parameter to potentially be patched too.

	// Change make(<type>[,<size>]) to MakeTrackedMap[<type>](0|<size>). It is
	// important not to patch over the size in case it recursively must be
	// patched.
	patches = append(patches, &superpose.Patch{
		Range: superpose.Range{Pos: call.Fun.Pos(), End: call.Lparen + 1},
		Str:   mapIterAlias + ".MakeTrackedMap[",
	})
	if len(call.Args) == 1 {
		// Add ending bracket and open paren with a 0 size
		patches = append(patches, &superpose.Patch{
			Range: superpose.Range{Pos: call.Args[0].End() + 1},
			Str:   "](0",
		})
	} else {
		// Add ending bracket and open paren squashing any potential comma
		patches = append(patches, &superpose.Patch{
			Range: superpose.Range{Pos: call.Args[0].End() + 1, End: call.Args[1].Pos()},
			Str:   "](",
		})
	}
	// Reset the line
	return append(patches, t.LineResetPatch(call))
}

func (t *transformInsertionPackage) transformDelete(call *ast.CallExpr, mapType *types.Map) []*superpose.Patch {
	// Change delete(<map>, <key>) to TrackedDelete(<map>, <key>). It is important
	// not to patch over the params in case they recursively must be patched.
	return []*superpose.Patch{{
		Range: superpose.RangeOf(call.Fun),
		Str:   mapIterAlias + ".TrackedDelete",
	}}
}

func (t *transformInsertionPackage) transformLit(
	lit *ast.CompositeLit,
	mapType *types.Map,
	stack []ast.Node,
) []*superpose.Patch {
	// Change <type>{<key1>:<val1>,<key2>:<val2>} to
	// NewTrackedMapLit[<type>](2).Put(<key1>,<val1>).Put(<key2>,<val2>).Done().
	// It is important we don't patch over and expressions in case they are
	// recursively patched. Also since nested literals don't have to put the type
	// before the key or value literal but we do, we have to walk the parent
	// composite literals to get the types we need for instantiation.
	panic("TODO")
}

func (t *transformInsertionPackage) transformPut(assn *ast.AssignStmt) []*superpose.Patch {
	// There are three kinds of assignments and we'll handle each differently:
	// 1. Single assignment of =
	// 2. Single assignment of <op>=
	// 3. Multi assignment of =
	//
	// As with others, we make sure not to overwrite expressions that may have
	// nested patches

	// For 1, change <map>[<k>] = <v> to TrackedPut(<map>, <k>, <v>)

	// For 2, change <map>[<k>] <op>= <v> to collapsed form of:
	// func() {
	//   __m, __k := <map>, <k>
	//   TrackedPut(__m, __k, __m[__k] <op> <v>)
	// }().
	// We have to use a func for hygiene and for places where only one statement
	// is allowed.

	// Change <map1>[<k1>], <map2>[<k2>], <other> = <v1>, <v2>, <v3> to collapsed
	// form of:
	// TrackedAssignMulti(func(__m *TrackedMultiAssign) {
	//   *__m.Key(<map1>, <k1>), *__m.Key(<map2>, <k2>, 1), <other> = __m.Val(<v1>), __m.Val(<v2>), <v3>
	// }).
	// We do this to keep the expressions in order and support single-statement
	// situations. Go evaluates all LHS before any RHS for indexes (like original
	// code) and pointer indirections (like this code). Those pointers are
	// worthless values as is the return from "Val", they're just there to
	// preserve order. The "Key" calls keep track of order and "Val" calls will
	// use the keys in order.
	panic("TODO")
}

func (t *transformInsertionPackage) transformRange(rang *ast.RangeStmt, mapType *types.Map) []*superpose.Patch {
	panic("TODO")
}
