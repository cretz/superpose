package superpose

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/tools/go/packages"
)

func (s *Superpose) compileDimensions(flags *compileFlags) error {
	pkgPath := flags.args[flags.pkgIndex]

	// Collect transformers that apply to this package
	transformers := make(map[string]Transformer, len(s.Config.Transformers))
	for dim, t := range s.Config.Transformers {
		ctx := &TransformContext{Context: context.Background(), Superpose: s, Dimension: dim}
		if applies, err := t.AppliesToPackage(ctx, pkgPath); err != nil {
			return err
		} else if applies {
			transformers[dim] = t
		}
	}

	// If there are no transformers, nothing to do
	if len(transformers) == 0 {
		return nil
	}

	// Load the action IDs for all referenced packages
	packageActionIDs, err := loadPackageActionIDs(pkgPath)
	if err != nil {
		return err
	}
	// Sanity check for this package's action ID
	buildID := flags.args[flags.buildIDIndex]
	if buildIDSlashIndex := strings.Index(buildID, "/"); buildIDSlashIndex < 0 {
		return fmt.Errorf("invalid build ID of %v", buildID)
	} else if actionID, err := base64.RawURLEncoding.DecodeString(buildID[:buildIDSlashIndex]); err != nil {
		return fmt.Errorf("failed decoding action ID: %w", err)
	} else if !bytes.Equal(actionID, packageActionIDs[pkgPath]) {
		return fmt.Errorf("expected action ID %v, but package action ID showed as %v", actionID, packageActionIDs[pkgPath])
	}

	// Load the package
	packagesLogf := s.Debugf
	if !s.Config.Verbose {
		packagesLogf = nil
	}
	pkgs, err := packages.Load(
		&packages.Config{
			Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
				packages.NeedImports | packages.NeedTypes | packages.NeedTypesSizes |
				packages.NeedSyntax | packages.NeedTypesInfo,
			Logf:  packagesLogf,
			Tests: true,
		},
		pkgPath,
	)
	if err != nil || len(pkgs) == 0 {
		return err
	}
	var pkg *packages.Package
	for _, maybePkg := range pkgs {
		// We'll debug-print any errors encountered, but we won't fail the build,
		// we'll let the downstream Go compiler give those errors
		if len(pkg.Errors) > 0 {
			for i, err := range pkg.Errors {
				s.Debugf("Failed loading package %v, error #%v: %v", pkgPath, i+1, err)
			}
			return nil
		}
		if maybePkg.PkgPath == pkgPath {
			// Sanity check
			if pkg != nil {
				return fmt.Errorf("package %v found twice", pkgPath)
			}
			pkg = maybePkg
			break
		}
	}
	if pkg == nil {
		return fmt.Errorf("package %v not found amongst %v loaded", pkgPath, len(pkgs))
	}

	// Perform transformation and compilation for each dimension
	for dim, transformer := range s.Config.Transformers {
		ctx := &TransformContext{Context: context.Background(), Superpose: s, Dimension: dim}

		// Collect user-defined patches
		userPatches, err := transformer.Transform(ctx, &TransformPackage{pkg})
		if err != nil {
			return fmt.Errorf("failed transforming %v to dimension %v: %w", pkgPath, dim, err)
		}

		// Patch imports and package name
		importPatches, err := s.transformPackageNameAndImports(ctx, pkg)
		if err != nil {
			return err
		}

		// Patch "<in>" bool vars
		boolVarPatches, err := s.transformInBoolVars(ctx, pkg)
		if err != nil {
			return err
		}

		// Compile the patches. Even if there aren't any, we need to perform the
		// compilation.
		patches := append(boolVarPatches, append(importPatches, userPatches...)...)
		if err := s.compilePatches(ctx, flags, pkg, patches); err != nil {
			return fmt.Errorf("compilation of patches to %v in dimension %v failed: %w", pkgPath, dim, err)
		}
	}
	return nil
}

func (s *Superpose) transformPackageNameAndImports(ctx *TransformContext, pkg *packages.Package) ([]*Patch, error) {
	panic("TODO")
}

func (s *Superpose) transformInBoolVars(ctx *TransformContext, pkg *packages.Package) ([]*Patch, error) {
	panic("TODO")
}

func (s *Superpose) compilePatches(
	ctx *TransformContext,
	flags *compileFlags,
	pkg *packages.Package,
	patches []*Patch,
) error {
	panic("TODO")
}
