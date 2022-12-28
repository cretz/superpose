package superpose

import (
	"context"
	"encoding/base64"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"golang.org/x/tools/go/packages"
)

func (s *Superpose) compileDimensions(ctx context.Context) error {
	// Collect transformers that apply to this package
	transformers := make(map[string]Transformer, len(s.Config.Transformers))
	for dim, t := range s.Config.Transformers {
		tctx := &TransformContext{Context: ctx, Superpose: s, Dimension: dim}
		// Confirm it applies to this package
		if applies, err := t.AppliesToPackage(tctx, s.pkgPath); err != nil {
			return err
		} else if !applies {
			continue
		}

		// Only add the transformer if cache is disabled or there is an error
		// getting the cached file (meaning it is not in cache or other issue)
		if !s.Config.ForceTransform {
			if file, fileCheckErr := s.dimDepPkgFile(s.pkgPath, dim); fileCheckErr == nil {
				s.Debugf("Skipping compiling %v in dimension %v, already cached at %v", s.pkgPath, dim, file)
				continue
			}
		}
		transformers[dim] = t
	}

	// If there are no transformers, nothing to do
	if len(transformers) == 0 {
		return nil
	}

	// Load the packages
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
			Tests: s.pkgForTest,
		},
		s.pkgPath,
	)
	if err != nil || len(pkgs) == 0 {
		return err
	}

	// Retain only the packages that match our expected path, doing sanity checks
	// along the way
	n := 0
	for _, pkg := range pkgs {
		// We'll debug-print any errors encountered, but we won't fail the build,
		// we'll let the downstream Go compiler give those errors
		if len(pkg.Errors) > 0 {
			for i, err := range pkg.Errors {
				s.Debugf("Failed loading package %v, error #%v: %v", s.pkgPath, i+1, err)
			}
			return nil
		} else if len(pkg.CompiledGoFiles) != len(pkg.Syntax) {
			// Sanity check to confirm files are same as compiled set
			return fmt.Errorf("package %v has %v compiled Go files, but only %v parsed",
				pkg.PkgPath, len(pkg.CompiledGoFiles), len(pkg.Syntax))
		} else if pkg.Fset != pkgs[0].Fset {
			// Sanity check to confirm the same fileset is used across all
			return fmt.Errorf("fileset pointers differ across packages unexpectedly")
		}

		// Keep all that match the path. This can be multiple in same-package test
		// case situations.
		if pkg.PkgPath == s.pkgPath {
			pkgs[n] = pkg
			n++
		}
	}
	pkgs = pkgs[:n]
	if len(pkgs) == 0 {
		return fmt.Errorf("package %v not found", s.pkgPath)
	}

	// Perform transformation and compilation for each dimension
	for dim, transformer := range s.Config.Transformers {
		tctx := &TransformContext{Context: ctx, Superpose: s, Dimension: dim}

		// 1:1 with packages
		results := make([]*TransformResult, len(pkgs))
		resultDimPkgRefs := dimPkgRefs{}
		for i, pkg := range pkgs {
			// Collect user-defined patches
			results[i], err = transformer.Transform(tctx, &TransformPackage{pkg})
			if err != nil {
				return fmt.Errorf("failed transforming %v to dimension %v: %w", s.pkgPath, dim, err)
			}

			// Patch imports
			importPatches, dimPkgRefs, err := s.transformImports(tctx, pkg)
			if err != nil {
				return err
			}
			results[i].Patches = append(results[i].Patches, importPatches...)
			resultDimPkgRefs.addAll(dimPkgRefs)

			// Patch "<in>" bool vars
			boolVarPatches, err := s.transformInBoolVars(tctx, pkg)
			if err != nil {
				return err
			}
			results[i].Patches = append(results[i].Patches, boolVarPatches...)

			// Patch line directives
			if results[i].AddLineDirectives {
				if err := s.addLineDirectives(tctx, pkg, results[i]); err != nil {
					return err
				}
			}
		}

		// Compile the patches. Even if there aren't any, we need to perform the
		// compilation.
		if err := s.compilePatches(tctx, pkgs, results, resultDimPkgRefs); err != nil {
			return fmt.Errorf("compilation of patches to %v in dimension %v failed: %w", s.pkgPath, dim, err)
		}
	}
	return nil
}

func (s *Superpose) transformImports(
	ctx *TransformContext,
	pkg *packages.Package,
) (patches []*Patch, pkgRefs dimPkgRefs, err error) {
	// Go over imports, replacing applicable ones w/ their dimension equivalents
	pkgRefs = dimPkgRefs{}
	for _, file := range pkg.Syntax {
		for _, mport := range file.Imports {
			if pkgPath, err := strconv.Unquote(mport.Path.Value); err != nil {
				return nil, nil, err
			} else if applies, err := s.Config.Transformers[ctx.Dimension].AppliesToPackage(ctx, pkgPath); err != nil {
				return nil, nil, err
			} else if applies {
				// Replace the import path but leave the alias. If the alias is not
				// present, explicitly set to what the package name was.
				var alias string
				if mport.Name != nil {
					alias = mport.Name.Name
				} else if importPkg := pkg.Imports[pkgPath]; importPkg == nil {
					return nil, nil, fmt.Errorf("missing import for %v", pkgPath)
				} else {
					alias = importPkg.Name
				}
				// Set patch and dimension package reference
				patches = append(patches, &Patch{
					Range: RangeOf(mport),
					Str:   fmt.Sprintf("%v %q", alias, s.DimensionPackagePath(pkgPath, ctx.Dimension)),
				})
				pkgRefs.addRef(pkgPath, ctx.Dimension)
			}
		}
	}
	return patches, pkgRefs, nil
}

func (s *Superpose) transformInBoolVars(ctx *TransformContext, pkg *packages.Package) ([]*Patch, error) {
	// Go over every var decl looking for a "//dim:<in>" bool var for replacing
	var patches []*Patch
	expectedComment := "//" + ctx.Dimension + ":<in>"
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			// Must be a "var <name> bool //dim:<in>" and nothing else
			decl, _ := decl.(*ast.GenDecl)
			if decl == nil || decl.Tok != token.VAR {
				continue
			}
			for _, spec := range decl.Specs {
				// Only vars w/ "//dim:<in>" comments
				spec, _ := spec.(*ast.ValueSpec)
				if spec == nil || spec.Comment == nil || len(spec.Comment.List) != 1 {
					continue
				} else if spec.Comment.List[0].Text != expectedComment {
					continue
				}
				// Now we know it's the expected comment, make sure the spec is proper
				if len(spec.Names) != 1 {
					return nil, fmt.Errorf("dimension in bool vars can only have a single identifier")
				} else if typ, _ := spec.Type.(*ast.Ident); typ == nil || typ.Name != "bool" {
					return nil, fmt.Errorf("dimension in bool var %v must have explicit bool type", spec.Names[0].Name)
				} else if len(spec.Values) != 0 {
					return nil, fmt.Errorf("dimension in bool var %v must not have a value already", spec.Names[0].Name)
				}
				// Add a patch to set it to true after the end of the bool part
				patches = append(patches, &Patch{Range: Range{Pos: spec.Type.End()}, Str: " = true"})
			}
		}
	}
	return patches, nil
}

func (s *Superpose) addLineDirectives(
	ctx *TransformContext,
	pkg *packages.Package,
	transformed *TransformResult,
) error {
	// Go over each existing patch, and add line directives the first time each
	// file is seen
	seenFiles := map[string]bool{}
	var lineDirectives []*Patch
	for _, patch := range transformed.Patches {
		// Get the file for the patch and ensure not already seen
		fileToken := pkg.Fset.File(patch.Range.Pos)
		if fileToken == nil {
			return fmt.Errorf("no file found for patch")
		} else if seenFiles[fileToken.Name()] {
			continue
		}
		// Find the syntax for the file. We have a sanity check earlier that ensures
		// CompiledGoFiles and Syntax are the same size
		var file *ast.File
		for i, goFile := range pkg.CompiledGoFiles {
			if goFile == fileToken.Name() {
				file = pkg.Syntax[i]
				break
			}
		}
		if file == nil {
			return fmt.Errorf("cannot find syntax for patched file %v", fileToken.Name())
		}
		seenFiles[fileToken.Name()] = true

		// Add the line directive to the end of the package
		lineDirectives = append(lineDirectives, &Patch{
			Range: Range{Pos: file.Package},
			Str:   fmt.Sprintf("/*line %v:%v*/", fileToken.Name(), pkg.Fset.Position(file.Package).Line),
		})
	}
	transformed.Patches = append(transformed.Patches, lineDirectives...)
	return nil
}

func (s *Superpose) compilePatches(
	ctx *TransformContext,
	pkgs []*packages.Package,
	transformed []*TransformResult,
	dimPkgRefs dimPkgRefs,
) error {
	// Copy the args
	args := make([]string, len(s.flags.args))
	copy(args, s.flags.args)

	// Patch files into temp files and update args
	tmpDir, err := s.UseTempDir()
	if err != nil {
		return err
	}
	for i, pkg := range pkgs {
		patchedFileBytes, err := ApplyPatches(pkg.Fset, transformed[i].Patches)
		if err != nil {
			return err
		}
		for origFile, newBytes := range patchedFileBytes {
			tmpFile, err := os.CreateTemp(tmpDir, "*-"+filepath.Base(origFile))
			if err != nil {
				return err
			}
			if transformed[i].LogPatchedFiles {
				s.Debugf("In dimension %v, patched %v to:\n%s", ctx.Dimension, origFile, newBytes)
			}
			_, err = tmpFile.Write(newBytes)
			if closeErr := tmpFile.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
			if err != nil {
				return err
			}
			// Update arg
			fileIndex, ok := s.flags.goFileIndexes[origFile]
			if !ok {
				return fmt.Errorf("cannot find expected file %v in compile args", origFile)
			}
			args[fileIndex] = tmpFile.Name()
		}
	}

	// Update -p to the dimension package ref
	args[s.flags.pkgIndex] = s.DimensionPackagePath(s.pkgPath, ctx.Dimension)

	// Update -o to a temp file that we'll put in cache later
	// TODO(cretz): Update trim path?
	args[s.flags.outputIndex] = filepath.Join(tmpDir, ctx.Dimension+"_pkg_.a")

	// Create a subkey of the action ID then create a new build ID that is
	// sub-action ID + "/" + sub-action ID. We use a subkey because the cached
	// item at the parent key is going to be the package itself after compilation.
	actionID, err := s.dimDepPkgActionID(s.pkgPath, ctx.Dimension)
	if err != nil {
		return err
	}
	s.hash.Reset()
	s.hash.Write(actionID)
	s.hash.Write([]byte("/superpose/for-compile"))
	compileActionIDStr := base64.RawURLEncoding.EncodeToString(s.hash.Sum(nil)[:len(actionID)])
	args[s.flags.buildIDIndex] = compileActionIDStr + "/" + compileActionIDStr

	// Update import cfg to replace original packages with their dimension
	// equivalents
	importCfg, err := s.loadImportCfg(args[s.flags.importCfgIndex])
	if err != nil {
		return fmt.Errorf("failed loading import cfg for compile: %w", err)
	} else if err := importCfg.updateDimPkgRefs(dimPkgRefs, true); err != nil {
		return fmt.Errorf("failed replacing dim package refs in compile import cfg: %w", err)
	}
	// Also include dependent packages
	seenDependentPackages := map[string]bool{}
	var metadata dimPkgMetadata
	for _, transformedRes := range transformed {
		for depPkg := range transformedRes.IncludeDependentPackages {
			if seenDependentPackages[depPkg] {
				continue
			}
			seenDependentPackages[depPkg] = true
			// Include in import config
			if err := importCfg.includePkg(depPkg); err != nil {
				return fmt.Errorf("failed including dependent package %v in dimension %v: %w", depPkg, ctx.Dimension, err)
			}
			// Include in metadata
			metadata.IncludeDependentPackages = append(metadata.IncludeDependentPackages, depPkg)
		}
	}
	// Write out import cfg
	if args[s.flags.importCfgIndex], err = importCfg.writeTempFile(); err != nil {
		return fmt.Errorf("failed creating compile import cfg: %w", err)
	}

	// Run compile
	s.Debugf("Running compile for dimension %v on package %v with args: %v", ctx.Dimension, s.pkgPath, args)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	// Copy the file to cache
	// TODO(cretz): Go source assumes seek for os.Open here, but we do not. That
	// means we have to copy everything into memory which is bad. Is there a
	// better way? How do they get away with it? Some VFS?
	b, err := os.ReadFile(args[s.flags.outputIndex])
	if err != nil {
		return err
	}
	cache, err := s.buildCache()
	if err != nil {
		return err
	}
	if err := cache.PutBytes(s.buildActionIDToCacheActionID(actionID), b); err != nil {
		return err
	}

	// Also put metadata in cache
	return s.setDimPkgMetadata(actionID, &metadata)
}
