package superpose

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strings"
)

type bridgeFile struct {
	fileName   string
	dimPkgRefs map[dimPkgRef]struct{}
}

type dimPkgRef struct {
	dim     string
	pkgPath string
}

// May return nil file which means no dimensions referenced
func (s *Superpose) buildBridgeFile(flags *compileFlags) (*bridgeFile, error) {
	// Get dimensions from every file
	builder := &bridgeFileBuilder{
		bridgeFile: bridgeFile{dimPkgRefs: map[dimPkgRef]struct{}{}},
		pkgPath:    flags.args[flags.pkgIndex],
		imports:    map[string]string{},
	}
	for goFile, _ := range flags.goFileIndexes {
		if ok, err := s.buildInitStatements(builder, goFile); err != nil {
			return nil, fmt.Errorf("failed building init statements for file %v: %w", goFile, err)
		} else if !ok {
			// Bail because of parsing issues
			return nil, nil
		}
	}

	// If there were no references, no bridge file
	if len(builder.dimPkgRefs) == 0 {
		return nil, nil
	}

	// Build code for the file
	code := "package " + builder.pkgName + "\n\n"
	for importPath, alias := range builder.imports {
		code += fmt.Sprintf("import %v %q\n", alias, importPath)
	}
	code += "\nfunc init() {\n"
	for _, stmt := range builder.initStatements {
		code += "\t" + stmt + "\n"
	}
	code += "}\n"

	// Write to a temp file
	tmpDir, err := s.UseTempDir()
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(tmpDir, "superpose-*.go")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	builder.fileName = f.Name()
	s.Debugf("Writing the following code to %v:\n%s\n", f.Name(), code)
	if _, err := f.Write([]byte(code)); err != nil {
		return nil, err
	}
	return &builder.bridgeFile, nil
}

type bridgeFileBuilder struct {
	bridgeFile
	pkgPath        string
	imports        map[string]string
	initStatements []string
	// Lazily populated on first file seen
	pkgName string
}

// Returns false with no error if we should bail because of parsing issues
func (s *Superpose) buildInitStatements(builder *bridgeFileBuilder, goFile string) (ok bool, err error) {
	// We load the file ahead of time here since we may manip later
	b, err := os.ReadFile(goFile)
	if err != nil {
		return false, err
	}
	// To save some perf, we're gonna look for the dimension comments anywhere in
	// file
	var foundDim string
	for dim := range s.Config.Transformers {
		if bytes.Contains(b, []byte("//"+dim+":")) {
			foundDim = dim
			break
		}
	}
	if foundDim == "" {
		return true, nil
	}

	// Parse so we can check dim references
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, goFile, b, parser.AllErrors|parser.ParseComments)
	// If there's an error parsing, we are going to ignore it because downstream
	// will show the error later
	if err != nil {
		s.Debugf("Ignoring %v, failed parsing: %v", goFile, err)
		return false, nil
	}

	// If the package is _test, fail. Otherwise, check/store package name
	if strings.HasSuffix(file.Name.Name, "_test") {
		return false, fmt.Errorf("cannot have dimensions in test files, found %v dimension in %v", foundDim, goFile)
	} else if builder.pkgName == "" {
		builder.pkgName = file.Name.Name
	} else if builder.pkgName != file.Name.Name {
		// Just ignore this, the actual compiler will report a better error
		s.Debugf("Ignoring %v, package %v different than expected %v", goFile, file.Name.Name, builder.pkgName)
		return false, nil
	}

	// Check each top-level var decl for dimension reference and build up
	// statements
	anyStatements := false
	for _, decl := range file.Decls {
		// Only var decl
		decl, _ := decl.(*ast.GenDecl)
		if decl == nil || decl.Tok != token.VAR {
			continue
		}
		for _, spec := range decl.Specs {
			// Only vars w/ comments
			spec, _ := spec.(*ast.ValueSpec)
			if spec == nil || spec.Comment == nil || len(spec.Comment.List) != 1 {
				continue
			}
			// Parse dim:ref
			pieces := strings.SplitN(spec.Comment.List[0].Text, ":", 2)
			if len(pieces) != 2 {
				continue
			}
			dim, ref := strings.TrimPrefix(pieces[0], "//"), pieces[1]
			t := s.Config.Transformers[dim]
			// If no transformer or only "<in>", does not apply to us
			if t == nil || ref == "<in>" {
				continue
			}
			// The transformer cannot be ignoring this package
			applies, err := t.AppliesToPackage(
				&TransformContext{Context: context.Background(), Superpose: s, Dimension: dim},
				builder.pkgPath,
			)
			if err != nil {
				return false, err
			} else if !applies {
				return false, fmt.Errorf("dimension %v referenced in package %v, but it is not applied", dim, builder.pkgPath)
			}

			// Validate the var decl
			if len(spec.Names) != 1 {
				return false, fmt.Errorf("dimension func vars can only have a single identifier")
			}
			funcType, _ := spec.Type.(*ast.FuncType)
			if funcType == nil {
				return false, fmt.Errorf("var %v is not typed with a func", spec.Names[0].Name)
			} else if len(spec.Values) != 0 {
				return false, fmt.Errorf("var %v cannot have default", spec.Names[0].Name)
			}

			// Find function in same file that is being referenced
			var funcDecl *ast.FuncDecl
			for _, maybeFuncDecl := range file.Decls {
				maybeFuncDecl, _ := maybeFuncDecl.(*ast.FuncDecl)
				if maybeFuncDecl != nil && maybeFuncDecl.Name.Name == ref && maybeFuncDecl.Recv == nil {
					funcDecl = maybeFuncDecl
					break
				}
			}
			if funcDecl == nil {
				return false, fmt.Errorf("unable to find func decl %v", ref)
			}

			// Confirm the signatures are identical (param names and everything). Just
			// do a string print of the types to confirm.
			emptyFset := token.NewFileSet()
			var expected, actual strings.Builder
			if err := printer.Fprint(&expected, emptyFset, funcType); err != nil {
				return false, err
			} else if err := printer.Fprint(&actual, emptyFset, funcDecl.Type); err != nil {
				return false, err
			} else if expected.String() != actual.String() {
				return false, fmt.Errorf("expected var %v to have type %v, instead had %v",
					spec.Names[0].Name, expected, actual)
			}

			// Now confirmed, add init statement
			s.Debugf("Setting var %v to function reference of %v in dimension %v", spec.Names[0].Name, ref, dim)
			dimPkgPath := s.DimensionPackage(builder.pkgPath, dim)
			builder.dimPkgRefs[dimPkgRef{dim: dim, pkgPath: dimPkgPath}] = struct{}{}
			importAlias := builder.importAlias(dimPkgPath)
			builder.initStatements = append(builder.initStatements,
				fmt.Sprintf("%v = %v.%v", spec.Names[0].Name, importAlias, ref))
			anyStatements = true
		}
	}

	// We expected at least one
	if !anyStatements {
		return false, fmt.Errorf("no proper dimension references found, though %v referenced", foundDim)
	}
	return true, nil
}

func (b *bridgeFileBuilder) importAlias(importPath string) string {
	alias := b.imports[importPath]
	if alias == "" {
		alias = fmt.Sprintf("import%v", len(b.imports)+1)
		b.imports[importPath] = alias
	}
	return alias
}
