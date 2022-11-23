package main

import (
	"fmt"
	"go/ast"
	"strings"
	"testing"

	"github.com/cretz/superpose"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

var unordered = map[bool]string{true: "foo", false: "bar"}
var ordered = map[string]string{"foo": "1", "bar": "2"}

func IterateMaps() {
	for k, v := range unordered {
		fmt.Println(k, v)
	}

	for k, v := range ordered {
		// Comment before print
		fmt.Println(k, v)
	}

	// Some middle comment

	// Comment before range
	for k, v := range unordered {
		fmt.Println(k, v)
	}
}

func TestTransformPackage(t *testing.T) {
	// Load the current package with tests
	pkgs, err := packages.Load(
		&packages.Config{
			Mode:  packages.LoadAllSyntax,
			Tests: true,
		},
		"github.com/cretz/superpose/example/maporder/superpose-maporder",
	)
	require.NoError(t, err)

	// Get the package and map func out that has main_test.go
	var pkg *packages.Package
	var compiledGoFile int
	var iterateMapsDecl *ast.FuncDecl
	for _, maybePkg := range pkgs {
		for i, goFile := range maybePkg.CompiledGoFiles {
			if strings.HasSuffix(goFile, "main_test.go") {
				pkg = maybePkg
				compiledGoFile = i
				ast.Inspect(pkg.Syntax[i], func(n ast.Node) bool {
					decl, _ := n.(*ast.FuncDecl)
					if decl != nil && decl.Name != nil && decl.Name.Name == "IterateMaps" {
						iterateMapsDecl = decl
						return false
					}
					return true
				})
				break
			}
		}
		if pkg != nil {
			break
		}
	}
	require.NotNil(t, pkg)
	require.NotNil(t, iterateMapsDecl)

	// Check code changes as expected
	patches, err := transformPackage(pkg)
	require.NoError(t, err)
	files, err := superpose.ApplyPatches(pkg.Fset, patches)
	require.NoError(t, err)
	require.Len(t, files, 1)
	file := string(files[pkg.CompiledGoFiles[compiledGoFile]])
	file = strings.ReplaceAll(file, "\r\n", "\n")
	require.NotEmpty(t, file)
	require.True(t, strings.HasPrefix(file,
		`package main; import __mapiter "github.com/cretz/superpose/example/maporder/superpose-maporder/mapiter"`+"\n"))
	require.Contains(t, file, `func IterateMaps() {
		for k, v := range __mapiter.PanicUnorderedKeys(unordered) {
						fmt.Println(k, v)
		}

		for __iter := __mapiter.NewSortedIter(ordered); __iter.Next(); { k, v := __iter.Pair()
						// Comment before print
						fmt.Println(k, v)
		}

		// Some middle comment

		// Comment before range
		for k, v := range __mapiter.PanicUnorderedKeys(unordered) {
						fmt.Println(k, v)
		}
}`)
}
