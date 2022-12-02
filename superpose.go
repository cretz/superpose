package superpose

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"hash"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/tools/go/packages"
)

type Config struct {
	// Required, and must be unique for each transformer change (this affects
	// cache)
	Version string
	// Keyed by dimension
	Transformers  map[string]Transformer
	Verbose       bool
	RetainTempDir bool
	// TODO(cretz): What is the default?
	CacheDir string

	// TODO(cretz): Allow customizing of load mode. If NeedDeps is present, import
	// packages could be reused instead of running load per package.
	// LoadMode: packages.LoadMode
}

type Superpose struct {
	Config  Config
	tempDir string
}

func RunMain(config Config, runConfig RunMainConfig) {
	if s, err := New(config); err != nil {
		log.Fatal(err)
	} else if err = s.RunMain(os.Args[1:], runConfig); err != nil {
		log.Fatal(err)
	}
}

func New(config Config) (*Superpose, error) {
	if config.Version == "" {
		return nil, fmt.Errorf("version required")
	} else if len(config.Transformers) == 0 {
		return nil, fmt.Errorf("at least one transformer required")
	}
	return &Superpose{Config: config}, nil
}

type RunMainConfig struct {
	AssumeToolexec  bool
	AdditionalFlags *flag.FlagSet
	AfterFlagParse  func(*Config)
}

func (s *Superpose) RunMain(args []string, config RunMainConfig) error {
	// Remove temp dir if present on complete and we're not retaining
	defer func() {
		if !s.Config.RetainTempDir && s.tempDir != "" {
			if err := os.RemoveAll(s.tempDir); err != nil {
				log.Printf("Warning, unable to remove temp dir %v", s.tempDir)
			}
		}
	}()

	// TODO(cretz): Support more approaches such as wrapping Go build or
	// go:generate or manual go build
	if !config.AssumeToolexec {
		return fmt.Errorf("only assume toolexec currently supported")
	}

	// Find index of first non-additional arg
	toolArgIndex := 0
	if config.AdditionalFlags != nil {
		for ; toolArgIndex < len(args); toolArgIndex++ {
			flagStr := args[toolArgIndex]
			if !strings.HasPrefix(flagStr, "-") {
				break
			}
			flagStr = strings.TrimLeft(flagStr, "-")
			eqIndex := strings.Index(flagStr, "=")
			if eqIndex >= 0 {
				flagStr = flagStr[:eqIndex]
			}
			// Make sure the flag exists and if not bool, skip arg if not set w/ "="
			flag := config.AdditionalFlags.Lookup(flagStr)
			if flag == nil {
				return fmt.Errorf("unrecognized flag %v", flagStr)
			}
			isBoolIface, _ := flag.Value.(interface{ IsBoolFlag() bool })
			if eqIndex == -1 && (isBoolIface == nil || !isBoolIface.IsBoolFlag()) {
				// Skip arg
				toolArgIndex++
			}
		}
	}

	// Confirm tool there and parse additional flags before checking tool
	if toolArgIndex >= len(args) {
		return fmt.Errorf("no tool name found")
	} else if config.AdditionalFlags != nil {
		if err := config.AdditionalFlags.Parse(args[:toolArgIndex]); err != nil {
			return err
		} else if config.AfterFlagParse != nil {
			config.AfterFlagParse(&s.Config)
		}
	}

	// We only care about compile
	importPath := os.Getenv("TOOLEXEC_IMPORTPATH")
	if importPath == "" {
		return fmt.Errorf("no import path found")
	}
	s.Debugf("Import path %v, args: %v", importPath, args)
	args = args[toolArgIndex:]
	_, tool := filepath.Split(args[0])
	if runtime.GOOS == "windows" {
		tool = strings.TrimSuffix(tool, ".exe")
	}

	// Go uses -V=full at first, so handle just that
	if len(args) == 2 && args[1] == "-V=full" {
		return s.compileVersionFull(tool, args)
	}

	// Run tool (using our custom args if compile)
	goToolArgs := args[1:]
	if tool == "compile" {
		var err error
		if goToolArgs, err = s.transformCompileArgs(goToolArgs, importPath); err != nil {
			return err
		}
		s.Debugf("Updated compile args to %v", goToolArgs)
	} else {
		s.Debugf("Skipping tool %v", tool)
	}

	cmd := exec.Command(args[0], goToolArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Not concurrency safe
func (s *Superpose) UseTempDir() (string, error) {
	if s.tempDir == "" {
		var err error
		if s.tempDir, err = os.MkdirTemp("", "superpose-build-"); err != nil {
			return "", err
		}
	}
	return s.tempDir, nil
}

func (s *Superpose) Debugf(f string, v ...interface{}) {
	if s.Config.Verbose {
		log.Printf(f, v...)
	}
}

func (s *Superpose) DimensionPackage(dimension string) string {
	// Just prefix with two underscores for now
	return "__" + dimension
}

func (s *Superpose) transformCompileArgs(args []string, importPath string) ([]string, error) {
	// We need to build dependencies for every dimension
	for dim, t := range s.Config.Transformers {
		if builder, err := s.newDimensionBuilder(dim, t); err != nil {
			return nil, err
		} else if err := builder.build(importPath); err != nil {
			return nil, fmt.Errorf("failed building dimension %v: %w", dim, err)
		}
	}

	// Now check every go file for dimension references and add an init file to
	// populate those vars from the dimension
	initBuilder := s.newDimensionInitBuilder()
	for i := len(args) - 1; i >= 0; i-- {
		// If we've hit a non-go file, we're done
		if strings.HasPrefix(args[i], "-") || !strings.HasSuffix(args[i], ".go") {
			break
		}
		// Add dimension references if needed
		if err := initBuilder.collectDimensionReferences(args[i], importPath); err != nil {
			return nil, fmt.Errorf("failed collecting dimension references from %v: %w", args[i], err)
		}
	}
	// If there are any init statements, write the file and add to end of compile
	if len(initBuilder.initStatements) > 0 {
		initFile, err := initBuilder.writeTempInitFile()
		if err != nil {
			return nil, err
		}
		args = append(args, initFile)
	}

	return args, nil
}

type dimensionBuilder struct {
	*Superpose
	packageActionIDs map[string][]byte
	cachedAppliesTo  map[string]bool

	dim         string
	transformer Transformer
	// Key is import path, value is dir
	handledImportPaths map[string]string
	hash               hash.Hash
}

func (s *Superpose) newDimensionBuilder(dim string, t Transformer) (*dimensionBuilder, error) {
	// TODO(cretz): "go list -export" to get build IDs for packages
	panic("TODO")
}

func (d *dimensionBuilder) build(importPath string) error {
	// If the action ID cannot be found or the import has already been handled,
	// do nothing
	actionID := d.packageActionIDs[importPath]
	if len(actionID) == 0 || d.handledImportPaths[importPath] != "" {
		return nil
	}

	// Check if applies
	ctx := &TransformContext{Context: context.Background(), Superpose: d.Superpose, Dimension: d.dim}
	if applies, err := d.appliesTo(ctx, importPath); !applies || err != nil {
		return err
	}

	// Build a hash of the package's action ID, this executable's content ID, and
	// the config's version get a cache key
	// TODO(cretz): Should compile-args play into the cache key?
	exeContentID, err := fetchExeContentID()
	if err != nil {
		return err
	}
	d.hash.Reset()
	d.hash.Write(actionID)
	d.hash.Write(exeContentID)
	d.hash.Write([]byte(d.Config.Version))
	cacheKeyBytes := d.hash.Sum(nil)[:15]

	// Prepare directory
	dimImportPath := path.Join(importPath, d.DimensionPackage(d.dim))
	cachedPackageDir := filepath.Join(d.Config.CacheDir, dimImportPath)
	// Append the base64'd action ID to the cache dir
	cachedPackageDir += "-" + base64.RawURLEncoding.EncodeToString(cacheKeyBytes)
	d.handledImportPaths[importPath] = cachedPackageDir
	// If the directory is already present this has already been built for this
	// cache key and we can move on
	if _, err := os.Stat(cachedPackageDir); err == nil {
		return nil
	}

	// Now that we are building, we need to load the package. We do not need deps'
	// syntax but we basically need everything else.
	packagesLogf := d.Debugf
	if !d.Config.Verbose {
		packagesLogf = nil
	}
	pkgs, err := packages.Load(
		&packages.Config{
			Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
				packages.NeedImports | packages.NeedTypes | packages.NeedTypesSizes |
				packages.NeedSyntax | packages.NeedTypesInfo,
			Logf: packagesLogf,
			// TODO(cretz): Load test packages too?
			// Tests: true,
		},
		importPath,
	)
	if err != nil || len(pkgs) == 0 {
		return err
	} else if len(pkgs) != 1 {
		return fmt.Errorf("expected only a single package for %v, got %v", importPath, len(pkgs))
	} else if len(pkgs[0].Errors) > 0 {
		// We'll debug-print any errors encountered, but we won't fail the build,
		// we'll let the downstream Go compiler give those errors
		for i, err := range pkgs[0].Errors {
			d.Debugf("Failed loading package %v, error #%v: %v", importPath, i+1, err)
		}
		return nil
	}
	pkg := pkgs[0]

	// Walk the deps first, transforming those before this
	for depImportPath := range pkg.Imports {
		if err := d.build(depImportPath); err != nil {
			return err
		}
	}

	// TODO(cretz):
	// * Run user transformer
	// * Change all import paths
	// * Set all "//dim:<in>" bool vars to true
	panic("TODO")
}

func (d *dimensionBuilder) appliesTo(ctx *TransformContext, importPath string) (bool, error) {
	applies, cached := d.cachedAppliesTo[importPath]
	if !cached {
		var err error
		if applies, err = d.transformer.AppliesToPackage(ctx, importPath); err != nil {
			return false, err
		}
		d.cachedAppliesTo[importPath] = applies
	}
	return applies, nil
}

type dimensionInitBuilder struct {
	*Superpose

	// Lazily set then checked every time thereafter
	packageName    string
	imports        map[string]string
	initStatements []string
}

func (s *Superpose) newDimensionInitBuilder() *dimensionInitBuilder {
	return &dimensionInitBuilder{Superpose: s, imports: map[string]string{}}
}

func (d *dimensionInitBuilder) collectDimensionReferences(goFile string, importPath string) error {
	// We load the file ahead of time here since we may manip later
	b, err := os.ReadFile(goFile)
	if err != nil {
		return err
	}
	// To save some perf, we're gonna look for the dimension comments anywhere in
	// file
	var foundDim string
	for dim := range d.Config.Transformers {
		if bytes.Contains(b, []byte("//"+dim+":")) {
			foundDim = dim
			break
		}
	}
	if foundDim == "" {
		return nil
	}

	// Parse so we can check dim references
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, goFile, b, parser.AllErrors|parser.ParseComments)
	// If there's an error parsing, we are going to ignore it because downstream
	// will show the error later
	if err != nil {
		d.Debugf("Ignoring %v, failed parsing: %v", goFile, err)
		return nil
	}

	// If the package is _test, fail. Otherwise, check/store package name
	if strings.HasSuffix(file.Name.Name, "_test") {
		return fmt.Errorf("cannot have dimensions in test files, found %v dimension in %v", foundDim, goFile)
	} else if d.packageName == "" {
		d.packageName = file.Name.Name
	} else if d.packageName != file.Name.Name {
		// Just ignore this, the actual compiler will report a better error
		d.Debugf("Ignoring %v, package %v different than expected %v", goFile, file.Name.Name, d.packageName)
		return nil
	}

	// Check each top-level var decl for dimension reference and build up
	// statements
	anyStatements := false
	for _, decl := range file.Decls {
		// Only var decl
		varDecl, _ := decl.(*ast.GenDecl)
		if varDecl == nil || varDecl.Tok != token.VAR {
			continue
		}
		for _, spec := range varDecl.Specs {
			// Only vars w/ comments
			valSpec, _ := spec.(*ast.ValueSpec)
			if valSpec == nil || valSpec.Comment == nil || len(valSpec.Comment.List) != 1 {
				continue
			}
			// Parse dim:ref
			pieces := strings.SplitN(valSpec.Comment.List[0].Text, ":", 2)
			if len(pieces) != 2 {
				continue
			}
			dim, ref := strings.TrimPrefix(pieces[0], "//"), pieces[1]
			t := d.Config.Transformers[dim]
			// If no transformer or only "<in>", does not apply to us
			if t == nil {
				continue
			}
			// The transformer cannot be ignoring this package
			applies, err := t.AppliesToPackage(
				&TransformContext{Context: context.Background(), Superpose: d.Superpose, Dimension: dim},
				importPath,
			)
			if err != nil {
				return err
			} else if !applies {
				return fmt.Errorf("dimension %v referenced in package %v, but it is not applied", dim, importPath)
			}

			// Validate the var decl
			if len(valSpec.Names) != 1 {
				return fmt.Errorf("dimension func vars can only have a single identifier")
			}
			funcType, _ := valSpec.Type.(*ast.FuncType)
			if funcType != nil {
				return fmt.Errorf("var %v is not typed with a func", valSpec.Names[0].Name)
			} else if len(valSpec.Values) != 0 {
				return fmt.Errorf("var %v cannot have default", valSpec.Names[0].Name)
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
				return fmt.Errorf("unable to find func decl %v", ref)
			}

			// Confirm the signatures are identical (param names and everything). Just
			// do a string print of the types to confirm.
			emptyFset := token.NewFileSet()
			var expected, actual strings.Builder
			if err := printer.Fprint(&expected, emptyFset, funcType); err != nil {
				return err
			} else if err := printer.Fprint(&actual, emptyFset, funcDecl.Type); err != nil {
				return err
			} else if expected.String() != actual.String() {
				return fmt.Errorf("expected var %v to have type %v, instead had %v",
					valSpec.Names[0].Name, expected, actual)
			}

			// Now confirmed, add init statement
			d.Debugf("Setting var %v to function reference of %v in dimension %v", valSpec.Names[0].Name, ref, dim)
			importAlias := d.importAlias(path.Join(importPath, d.DimensionPackage(dim)))
			d.initStatements = append(d.initStatements,
				fmt.Sprintf("%v = %v.%v", valSpec.Names[0].Name, importAlias, ref))
			anyStatements = true
		}
	}

	// We expected at least one
	if !anyStatements {
		return fmt.Errorf("no proper dimension references found, though %v referenced", foundDim)
	}
	return nil
}

func (d *dimensionInitBuilder) importAlias(importPath string) string {
	alias := d.imports[importPath]
	if alias == "" {
		alias = fmt.Sprintf("import%v", len(d.imports)+1)
		d.imports[importPath] = alias
	}
	return alias
}

func (d *dimensionInitBuilder) writeTempInitFile() (string, error) {
	// Build code
	code := "package " + d.packageName + "\n\n"
	for alias, importPath := range d.imports {
		code += fmt.Sprintf("import %v %q\n", alias, importPath)
	}
	code += "\nfunc init() {\n"
	for _, stmt := range d.initStatements {
		code += "\t" + stmt + "\n"
	}
	code += "}\n"

	// Write to a temp file
	tmpDir, err := d.UseTempDir()
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(tmpDir, "superpose-*.go")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write([]byte(code)); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func (s *Superpose) compileVersionFull(tool string, args []string) error {
	// Go build uses the results of this to know whether to recompile. This is
	// usually to Go compiler version. We add the user version and our version to
	// this. Some of this code taken from Garble.

	// Get Go's tool ID
	goOutLine, goToolID, err := loadGoToolID(tool, args)
	if err != nil {
		return err
	}

	// Get this exe's content ID
	exeContentID, err := fetchExeContentID()
	if err != nil {
		return err
	}

	// Build a hash of slash-delimited Go tool ID + this executable's content ID +
	// user version
	// TODO(cretz): What about additional flags here?
	h := sha256.New()
	h.Write(goToolID)
	h.Write([]byte("/"))
	h.Write(exeContentID)
	h.Write([]byte("/"))
	h.Write([]byte(s.Config.Version))
	// Go only allows a certain size
	contentID := base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:15])

	// Append content ID as end of fake build ID
	fmt.Printf("%s +superpose buildID=_/_/_/%s\n", goOutLine, contentID)
	return nil
}

func loadGoToolID(tool string, args []string) (line string, b []byte, err error) {
	// Most of this taken from Garble
	cmd := exec.Command(args[0], args[1:]...)
	b, err = cmd.Output()
	if err != nil {
		if err, _ := err.(*exec.ExitError); err != nil {
			return "", nil, fmt.Errorf("%v: %s", err, err.Stderr)
		}
		return "", nil, err
	}
	line = string(bytes.TrimSpace(b))
	f := strings.Fields(line)
	if len(f) < 3 || f[0] != tool || f[1] != "version" ||
		(f[2] == "devel" && !strings.HasPrefix(f[len(f)-1], "buildID=")) {
		return "", nil, fmt.Errorf("%s -V=full: unexpected output:\n\t%s", args[0], line)
	}
	if f[2] == "devel" {
		// On the development branch, use the content ID part of the build ID.
		buildID := f[len(f)-1]
		contentID := buildID[strings.LastIndex(buildID, "/")+1:]
		b, err = base64.RawURLEncoding.DecodeString(contentID)
	} else {
		// For a release, the output is like: "compile version go1.9.1 X:framepointer".
		// Use the whole line, as we can assume it's unique.
		b = []byte(line)
	}
	return
}

var exeContentID []byte

func fetchExeContentID() ([]byte, error) {
	if len(exeContentID) == 0 {
		// Most of this taken from Garble
		exePath, err := os.Executable()
		if err != nil {
			return nil, err
		}
		cmd := exec.Command("go", "tool", "buildid", exePath)
		out, err := cmd.Output()
		if err != nil {
			if err, _ := err.(*exec.ExitError); err != nil {
				return nil, fmt.Errorf("%v: %s", err, err.Stderr)
			}
			return nil, err
		}
		buildID := string(out)
		contentID := buildID[strings.LastIndex(buildID, "/")+1:]
		exeContentID, err = base64.RawURLEncoding.DecodeString(contentID)
		if err != nil {
			return nil, err
		}
	}
	return exeContentID, nil
}
