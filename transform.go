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

// Transformer is the interface all dimension transformers must implement.
type Transformer interface {
	// AppliesToPackage is called each time Superpose needs to know whether this
	// dimension applies to the given package. This should not be an expensive
	// call since it is called many times by Superpose.
	//
	// When false is returned, `Transform`` will not be called for this package.
	AppliesToPackage(ctx *TransformContext, pkgPath string) (bool, error)

	// Transform returns a [TransformResult] containing patches to the given
	// package. The `pkg` should never be mutated by this function. The result may
	// be mutated after this call is returned, so a reference to it should not be
	// held.
	//
	// When error is nil, the result should be non-nil, even if there are no
	// patches.
	Transform(ctx *TransformContext, pkg *TransformPackage) (*TransformResult, error)
}

// TransformContext is a dimension-specific context used for transformer calls.
type TransformContext struct {
	// Context is the embedded Go context. This context usually just comes from
	// [RunMain] and is not closed on completion.
	context.Context

	// Superpose is the overall [Superpose] instance.
	Superpose *Superpose

	// Dimension is the current dimension being transformed.
	Dimension string
}

// TransformPackage is the package to transform. This currently just embeds
// [packages.Package] and should never be mutated.
type TransformPackage struct {
	*packages.Package
}

// TransformResult represents a result of a transform.
type TransformResult struct {
	// Patches contains the set of patches to apply. This cannot overlap and the
	// patches should not replace dimension-applicable import paths (the internal
	// system does that).
	Patches []*Patch

	// IncludeDependencyPackages is a set of packages that should be included on
	// the transformed code that may not have been included in the original code.
	// this is important for the Go compiler/linker since they can't otherwise
	// know ahead of time what the new dependencies are. Since these reference Go
	// cache during build, these modules should already be built and in the Go
	// cache.
	IncludeDependencyPackages map[string]struct{}

	// AddLineDirectives, if true, will add a line directive to the top of each
	// patched Go file informing the Go compiler that the dimension filename is
	// actually the original filename. This can help with stack traces and
	// debugging, but transformers should be careful to not alter line numbers
	// with their patches.
	AddLineDirectives bool

	// LogPatchedFiles, if true, does a debug log of fully patched file before
	// writing it. The logs will only be visible when `Verbose` config is true.
	LogPatchedFiles bool

	// TODO(cretz): Allow customizing of load mode? Per transformer?
	// LoadMode: packages.LoadMode
}

// Patch represents a patch to a file.
type Patch struct {
	// Range represents the range to patch. If `End` is 0/unset, this patch is an
	// insert instead of a replace.
	Range Range

	// Captures is the set of captures to take from the original file to be used
	// in an `Str` template (see below).
	Captures map[string]Range

	// Str is the string to replace with. If there are any "{{", this is a Go
	// template where the map keys are indices of the `Captures` and the values
	// the captured strings.
	Str string
}

// WrapWithPatch creates a patch that adds the lhs and rhs values on either side
// of the given node.
func WrapWithPatch(n ast.Node, lhs, rhs string) *Patch {
	r := RangeOf(n)
	return &Patch{Range: r, Captures: map[string]Range{"__1__": r}, Str: lhs + "{{.__1__}}" + rhs}
}

// ApplyPatches applies the given patches to the fileset and returns a map of
// only affected files and their final contents. Note, this function may reorder
// the given patches slice.
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
		if i > 0 && patches[i-1].Range.Overlaps(&patch.Range) {
			return nil, fmt.Errorf("patches overlap")
		}
		if err := ApplyPatch(fset, patch, files); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// ApplyPatch applies a single patch based on the given fileset, and then sets
// the resulting content in the files map parameter.
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

// Range is a range of positions in Go source.
type Range struct {
	// Pos is the inclusive start position. Required.
	Pos token.Pos

	// End is the exclusive end position. In some uses, `End` can be 0/unset to
	// mean only a single position based on `Pos`.
	End token.Pos
}

// Overlaps returns true if this range overlaps the other range in any way.
func (r *Range) Overlaps(other *Range) bool {
	// Check that current pos/end isn't inside the other range or vice versa
	return r.Contains(other.Pos) ||
		(other.End > other.Pos && r.Contains(other.End-1)) ||
		other.Contains(r.Pos) ||
		(r.End > r.Pos && other.Contains(r.End-1))
}

// Contains returns true if the given position is in this range.
func (r *Range) Contains(p token.Pos) bool {
	// If there's no end, we only need to check if it's exactly the start
	if !r.End.IsValid() {
		return p == r.Pos
	}
	return r.Pos <= p && r.End > p
}

// RangeOf gives the range for the given node.
func RangeOf(x ast.Node) Range {
	return Range{Pos: x.Pos(), End: x.End()}
}
