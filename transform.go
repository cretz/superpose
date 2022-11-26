package superpose

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"sort"
	"strings"
	"text/template"

	"golang.org/x/tools/go/packages"
)

type TransformContext struct {
	context.Context
	Superpose *Superpose
	Dimension string
}

type TransformPackage struct {
	*packages.Package
	GoBuildID          string
	CurrentlyCompiling bool
	Transformed        bool
	// If true, this is already cached and any transform is ignored
	Cached bool
}

type TransformedPackage struct {
	ID string
	// Keyed by FileSet's file name. This should not be used to patch imports.
	// TODO(cretz): Then how can they patch imports?
	Patches []*Patch

	// TODO(cretz): NewFiles map[string]string
	// TODO(cretz): DeleteFiles []string
}

type Range struct{ Pos, End token.Pos }

func RangeOf(x ast.Node) Range {
	return Range{Pos: x.Pos(), End: x.End()}
}

type Patch struct {
	// 0 End means just insert, no replace
	Range    Range
	Captures map[string]Range
	// If there are any "{{", this is a Go template where the map keys are indices
	// of the Captures and the values the captured strings
	Str string
}

func WrapWithPatch(n ast.Node, lhs, rhs string) *Patch {
	r := RangeOf(n)
	return &Patch{Range: r, Captures: map[string]Range{"__1__": r}, Str: lhs + "{{.__1__}}" + rhs}
}

type Transformer interface {
	AppliesToPackage(ctx *TransformContext, pkg string) (bool, error)
	Transform(ctx *TransformContext, pkgs []*TransformPackage) ([]*TransformedPackage, error)
}

// May reorder slice
func ApplyPatches(fset *token.FileSet, patches []*Patch) (map[string][]byte, error) {
	// Sort in reverse order
	sort.Slice(patches, func(i, j int) bool { return patches[i].Range.Pos > patches[j].Range.Pos })
	// Apply in reverse order, validating range each time
	files := map[string][]byte{}
	for i, patch := range patches {
		if !patch.Range.Pos.IsValid() {
			return nil, fmt.Errorf("patch missing start pos")
		} else if patch.Range.End.IsValid() && patch.Range.End < patch.Range.Pos {
			return nil, fmt.Errorf("patch end before start")
		}
		if i > 0 {
			prev := patches[i-1]
			if patch.Range.Pos >= prev.Range.Pos || (patch.Range.End.IsValid() && patch.Range.End >= prev.Range.Pos) {
				return nil, fmt.Errorf("patches overlap")
			}
		}
		if err := ApplyPatch(fset, patch, files); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func ApplyPatch(fset *token.FileSet, patch *Patch, files map[string][]byte) error {
	// Load file if not already there
	file := fset.File(patch.Range.Pos)
	if file == nil {
		return fmt.Errorf("cannot find file for patch")
	}
	fileBytes := files[file.Name()]
	if len(fileBytes) == 0 {
		var err error
		if fileBytes, err = os.ReadFile(file.Name()); err != nil {
			return fmt.Errorf("failed reading file %v: %w", file.Name(), err)
		}
		files[file.Name()] = fileBytes
	}

	// If str is a template, apply
	str := patch.Str
	if strings.Contains(str, "{{") {
		t, err := template.New("patch").Parse(str)
		if err != nil {
			return fmt.Errorf("failed parsing template: %w", err)
		}
		captureMap := make(map[string]string, len(patch.Captures))
		for k, capture := range patch.Captures {
			start := fset.Position(capture.Pos)
			end := fset.Position(capture.End)
			if !start.IsValid() || !end.IsValid() || start.Filename != file.Name() || end.Filename != file.Name() {
				return fmt.Errorf("start or end invalid or in wrong file")
			}
			captureMap[k] = string(fileBytes[start.Offset:end.Offset])
		}
		var bld strings.Builder
		if err := t.Execute(&bld, captureMap); err != nil {
			return fmt.Errorf("failed running template: %w", err)
		}
		str = bld.String()
	}

	// Replace in file bytes
	start := fset.Position(patch.Range.Pos).Offset
	end := start
	if patch.Range.End.IsValid() {
		end = fset.Position(patch.Range.End).Offset
	}
	files[file.Name()] = append(fileBytes[:start], append([]byte(str), fileBytes[end:]...)...)
	return nil
}
