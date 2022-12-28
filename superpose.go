package superpose

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"hash"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rogpeppe/go-internal/cache"
)

// Config is configuration for a [Superpose] instance.
type Config struct {
	// Version is any value that signifies the version of this set of
	// transformers. This version is used in hashes for caching builds. The
	// version should be unique per deployed transformer set and updated each time
	// any change is made. Otherwise a cached build from a previous build may be
	// used.
	//
	// Required.
	Version string

	// Transformers are the set of transformers keyed by dimension name.
	//
	// At least one required.
	Transformers map[string]Transformer

	// Verbose, if true, will log many details during compilation.
	Verbose bool

	// RetainTempDir, if true, will not delete the temporary directory on
	// completion. Otherwise, the temporary directory is deleted each run.
	RetainTempDir bool

	// BuildCacheDir is the cache directory to use for caching build output. The
	// default is [os.UserCacheDir]()/superpose-build.
	BuildCacheDir string

	// ForceTransform, if true, will always transform and compile dimension
	// packages even if they are already cached. Note, this still uses/updates the
	// cache, it just doesn't skip if already cached.
	ForceTransform bool
}

// Superpose is an instance of the currently running toolexec.
//
// No methods on this struct are safe for concurrent use.
type Superpose struct {
	// Config is the configuration given on start.
	Config Config

	pkgPath     string
	pkgForTest  bool
	origCLIArgs []string
	tool        string
	// Only properly set after we know we're at the compile step
	flags compileFlags
	hash  hash.Hash
	// Lazy, use buildCache()
	_buildCache *cache.Cache
	// Lazy, use depPkgActionIDs()
	_depPkgActionIDs map[string][]byte
	// Lazy, use UseTempDir()
	_tempDir string
}

// RunMainConfig is configuration for [RunMain].
type RunMainConfig struct {
	// AssumeToolexec, if true, assumes the main run is as `-toolexec`.
	//
	// Currently required as true since toolexec form is the only currently
	// supported form.
	AssumeToolexec bool

	// AdditionalFlags, if set, are additional flags that can be passed to this
	// toolexec. They are removed from the upstream arguments.
	AdditionalFlags *flag.FlagSet

	// AfterFlagParse, if set and `AdditionalFlags` is set, is called once flags
	// have been parsed.
	AfterFlagParse func(*Config)
}

// RunMain runs the configured Superpose tool. This is just [New] +
// [Superpose.RunMain].
func RunMain(ctx context.Context, config Config, runConfig RunMainConfig) {
	if s, err := New(config); err != nil {
		log.Fatal(err)
	} else if err = s.RunMain(ctx, os.Args[1:], runConfig); err != nil {
		log.Fatal(err)
	}
}

// New creates a new [Superpose] instance for the given config.
func New(config Config) (*Superpose, error) {
	if config.Version == "" {
		return nil, fmt.Errorf("version required")
	} else if len(config.Transformers) == 0 {
		return nil, fmt.Errorf("at least one transformer required")
	} else if sha256.Size != cache.HashSize {
		return nil, fmt.Errorf("cache library no longer uses expected hash size")
	}
	s := &Superpose{
		Config:  config,
		pkgPath: os.Getenv("TOOLEXEC_IMPORTPATH"),
		hash:    sha256.New(),
	}
	// The import path may be "foo [foo.test]" for tests, so we check that here.
	// We have confirmed with Go impl that import paths cannot contain spaces.
	spaceIndex := strings.Index(s.pkgPath, " ")
	if spaceIndex > 0 {
		if !strings.HasSuffix(s.pkgPath, ".test]") {
			return nil, fmt.Errorf("assuming test because space in package path, but got %v", s.pkgPath)
		}
		s.pkgPath = s.pkgPath[:spaceIndex]
		s.pkgForTest = true
	}
	return s, nil
}

// RunMain runs this Superpose tool for the given args and config.
func (s *Superpose) RunMain(ctx context.Context, args []string, config RunMainConfig) error {
	// Cleanup the cache on complete if it's present (meaning it was used)
	defer func() {
		if s._buildCache != nil {
			s._buildCache.Trim()
		}
	}()

	// Remove temp dir if present on complete and we're not retaining
	defer func() {
		if !s.Config.RetainTempDir && s._tempDir != "" {
			if err := os.RemoveAll(s._tempDir); err != nil {
				log.Printf("Warning, unable to remove temp dir %v", s._tempDir)
			}
		}
	}()

	// Set original args
	s.origCLIArgs = args

	// TODO(cretz): Support more approaches such as wrapping Go build or
	// go:generate or manual go build
	if !config.AssumeToolexec {
		return fmt.Errorf("only assume toolexec currently supported")
	}

	// If the first arg is --verbose, set as verbose and remove the arg
	if args[0] == "--verbose" {
		s.Config.Verbose = true
		args = args[1:]
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

	// Get import path and tool exe
	args = args[toolArgIndex:]
	_, s.tool = filepath.Split(args[0])
	if runtime.GOOS == "windows" {
		s.tool = strings.TrimSuffix(s.tool, ".exe")
	}

	// Go uses -V=full at first, so handle just that
	if len(args) == 2 && args[1] == "-V=full" {
		return s.toolexecVersionFull(s.tool, args)
	}

	// Henceforth, we expect a package
	if s.pkgPath == "" {
		return fmt.Errorf("no TOOLEXEC_IMPORTPATH env var")
	}

	s.Debugf("Intercepting toolexec with import path %q and args: %v", os.Getenv("TOOLEXEC_IMPORTPATH"), args)
	switch s.tool {
	case "compile":
		var err error
		if args, err = s.onCompile(ctx, args); err != nil {
			return err
		}
		s.Debugf("Updated compile args to %v", args)
	case "link":
		if err := s.onLink(ctx, args); err != nil {
			return err
		}
	default:
		s.Debugf("No interception needed for tool %v", s.tool)
	}

	// Run the command
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// UseTempDir returns the temporary directory for use during this process. The
// temporary directory is usually deleted at the end of the run. The temporary
// is lazily created when this is first called, hence the error result.
func (s *Superpose) UseTempDir() (string, error) {
	if s._tempDir == "" {
		var err error
		if s._tempDir, err = os.MkdirTemp("", "superpose-build-"); err != nil {
			return "", err
		}
	}
	return s._tempDir, nil
}

// Debugf logs a debug statement if verbose config is set.
func (s *Superpose) Debugf(f string, v ...interface{}) {
	if s.Config.Verbose {
		log.Printf(f, v...)
	}
}

// DimensionPackagePath returns the fully qualified package path for the given
// package path in the given dimension.
func (s *Superpose) DimensionPackagePath(origPkg string, dimension string) string {
	// Just delimit with two underscores for now
	return origPkg + "__" + dimension
}

func (s *Superpose) onCompile(ctx context.Context, args []string) (newArgs []string, err error) {
	// Parse flags
	if err := s.flags.parse(args); err != nil {
		return nil, err
	}

	// Compile dimensions
	if err := s.compileDimensions(ctx); err != nil {
		return nil, err
	}

	// Create bridge file if needed. If no bridge file, just reuse the same args.
	bridgeFile, err := s.buildBridgeFile(ctx)
	if bridgeFile == nil || err != nil {
		return args, err
	}

	// Now that we know there is a bridge file, copy all the args and add bridge
	// file to the end
	newArgs = make([]string, len(args)+1)
	copy(newArgs, args)
	newArgs[len(newArgs)-1] = bridgeFile.fileName

	// Update import cfg to include the dimension package references
	if importCfg, err := s.loadImportCfg(newArgs[s.flags.importCfgIndex]); err != nil {
		return nil, fmt.Errorf("failed loading import cfg for bridge: %w", err)
	} else if err := importCfg.updateDimPkgRefs(bridgeFile.dimPkgRefs, false); err != nil {
		return nil, fmt.Errorf("failed updating dim package refs in bridge import cfg: %w", err)
	} else if err := importCfg.writeFile(newArgs[s.flags.importCfgIndex]); err != nil {
		return nil, fmt.Errorf("failed creating bridge import cfg: %w", err)
	}

	// Note, we don't have to alter the build ID for the compile args because the
	// build ID already takes into account the global build ID with our version
	// from -V=full.
	return newArgs, nil
}

func (s *Superpose) onLink(ctx context.Context, args []string) error {
	// Go over every package file in the import cfg and add entries for every
	// missing dimension reference.

	// Load the import config
	var importCfgFile string
	for i, arg := range args {
		if arg == "-importcfg" {
			importCfgFile = args[i+1]
			break
		}
	}
	if importCfgFile == "" {
		return fmt.Errorf("no import cfg file for link")
	}
	importCfg, err := s.loadImportCfg(importCfgFile)
	if err != nil {
		return fmt.Errorf("failed loading link import cfg: %w", err)
	}

	// Walk every line, collecting dimension equivalents
	dimPkgRefs := dimPkgRefs{}
	for _, line := range importCfg.lines {
		if !strings.HasPrefix(line, "packagefile ") {
			continue
		}
		origPkgPath := strings.TrimPrefix(line[:strings.Index(line, "=")], "packagefile ")
		// Do not include the ".test" special package
		// TODO(cretz): What if there's a legit ".test" package?
		if strings.HasSuffix(origPkgPath, ".test") {
			continue
		}
		for dim, t := range s.Config.Transformers {
			// Confirm applies
			applies, err := t.AppliesToPackage(
				&TransformContext{Context: ctx, Superpose: s, Dimension: dim}, origPkgPath)
			if err != nil {
				return fmt.Errorf("failed determining whether package %v applies during link: %w", origPkgPath, err)
			} else if !applies {
				continue
			}

			// Load metadata for the package
			actionID, err := s.dimDepPkgActionID(origPkgPath, dim)
			if err != nil {
				return err
			}
			metadata, err := s.getDimPkgMetadata(actionID)
			if err != nil {
				return fmt.Errorf("failed getting metadata for package %v in dimension %v: %w", origPkgPath, dim, err)
			}

			// Add the reference to import cfg
			dimPkgRefs.addRef(origPkgPath, dim)

			// Include dependent packages
			for _, depPkg := range metadata.IncludeDependentPackages {
				if err := importCfg.includePkg(depPkg); err != nil {
					return fmt.Errorf("failed including dependent %v package for package %v in dimension %v: %w",
						depPkg, origPkgPath, dim, err)
				}
			}
		}
	}

	// If there are any dimension references, update import cfg
	if len(dimPkgRefs) > 0 {
		if err := importCfg.updateDimPkgRefs(dimPkgRefs, false); err != nil {
			return fmt.Errorf("failed updating dim package refs for link: %w", err)
		}
		return importCfg.writeFile(importCfgFile)
	}
	return nil
}

func (s *Superpose) dimDepPkgActionID(origPkg string, dim string) ([]byte, error) {
	// Get the original package action ID and make a subkey
	pkgActionIDs, err := s.depPkgActionIDs()
	if err != nil {
		return nil, err
	}
	pkgActionID, ok := pkgActionIDs[origPkg]
	if !ok {
		return nil, fmt.Errorf("unable to find action ID for package %v", origPkg)
	}
	return s.dimPkgActionID(pkgActionID, dim), nil
}

func (s *Superpose) dimPkgActionID(origPkgActionID []byte, dim string) []byte {
	s.hash.Reset()
	s.hash.Write(origPkgActionID)
	s.hash.Write([]byte("/superpose/"))
	s.hash.Write([]byte(dim))
	s.hash.Write([]byte("/"))
	s.hash.Write([]byte(s.Config.Version))
	return s.hash.Sum(nil)[:len(origPkgActionID)]
}

// Errors or gives string file, never empty string with no error
func (s *Superpose) dimDepPkgFile(origPkg string, dim string) (string, error) {
	actionID, err := s.dimDepPkgActionID(origPkg, dim)
	if err != nil {
		return "", err
	}
	cache, err := s.buildCache()
	if err != nil {
		return "", err
	}
	// An action ID for cache purposes is not the same as the package action ID
	// from the package build ID
	file, _, err := cache.GetFile(s.buildActionIDToCacheActionID(actionID))
	if err != nil {
		return "", fmt.Errorf("failed getting action ID for pkg %v in dimension %v: %w", origPkg, dim, err)
	}
	return file, nil
}

// Errors or gives string file, never empty string with no error
func (s *Superpose) pkgFile(pkgPath string) (string, error) {
	cmd := exec.Command("go", "list", "-f", "{{.Export}}", "-export", pkgPath)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed getting export for %v: %w. Output: %s", pkgPath, err, b)
	}
	ret := strings.TrimSpace(string(b))
	if ret == "" {
		return "", fmt.Errorf("no package file export for %v", pkgPath)
	}
	return ret, nil
}

type dimPkgMetadata struct {
	IncludeDependentPackages []string `json:"includeDependentPackages"`
}

func (s *Superpose) getDimPkgMetadata(actionID []byte) (*dimPkgMetadata, error) {
	cache, err := s.buildCache()
	if err != nil {
		return nil, err
	}
	b, _, err := cache.GetBytes(s.dimPkgMetadataCacheID(actionID))
	if err != nil {
		return nil, err
	}
	var metadata dimPkgMetadata
	if err := json.Unmarshal(b, &metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func (s *Superpose) setDimPkgMetadata(actionID []byte, metadata *dimPkgMetadata) error {
	b, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	cache, err := s.buildCache()
	if err != nil {
		return err
	}
	return cache.PutBytes(s.dimPkgMetadataCacheID(actionID), b)
}

func (s *Superpose) dimPkgMetadataCacheID(actionID []byte) (cacheActionID cache.ActionID) {
	s.hash.Reset()
	s.hash.Write(actionID)
	s.hash.Write([]byte("/superpose/metadata"))
	s.hash.Sum(cacheActionID[:0])
	return
}

func (s *Superpose) buildActionIDToCacheActionID(buildActionID []byte) (cacheActionID cache.ActionID) {
	// Just re-hash
	s.hash.Reset()
	s.hash.Write(buildActionID)
	s.hash.Write([]byte("/superpose"))
	s.hash.Sum(cacheActionID[:0])
	return
}

func (s *Superpose) buildCache() (*cache.Cache, error) {
	if s._buildCache == nil {
		// Use subdir of user cache dir if not set
		cacheDir := s.Config.BuildCacheDir
		if cacheDir == "" {
			userCacheDir, err := os.UserCacheDir()
			if err != nil {
				return nil, fmt.Errorf("failed getting user cache dir: %w", err)
			}
			cacheDir = filepath.Join(userCacheDir, "superpose-build")
		}
		// Create the dir if not present
		if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
			if err := os.MkdirAll(cacheDir, 0777); err != nil {
				return nil, fmt.Errorf("failed creating cache dir: %w", err)
			}
		}
		var err error
		if s._buildCache, err = cache.Open(cacheDir); err != nil {
			return nil, fmt.Errorf("failed opening build cache at %v: %w", cacheDir, err)
		}
	}
	return s._buildCache, nil
}

func (s *Superpose) depPkgActionIDs() (map[string][]byte, error) {
	if s._depPkgActionIDs == nil {
		// Use "go list" to get action IDs for this package and every dependency.
		// During compile, pkgPath is a legit package path, but during link
		// sometimes it is not (sometimes it "command-line-arguments" or the test
		// package). So during link we use importcfg to know dependents.
		// TODO(cretz): Why not change to always using importcfg?
		args := []string{"list", "-f", "{{.ImportPath}}|{{.BuildID}}", "-export"}
		if s.pkgPath != "command-line-arguments" {
			pkgPath, forTest := s.pkgPath, s.pkgForTest
			if strings.HasSuffix(pkgPath, ".test") {
				pkgPath, forTest = strings.TrimSuffix(pkgPath, ".test"), true
			}
			if forTest {
				args = append(args, "-test")
			}
			args = append(args, "-deps", pkgPath)
		} else {
			// We want to ignore missing packages here since link has some
			// dependencies that are not real packages
			args = append(args, "-e")
			// TODO(cretz): Cache since this is reused by link
			// TODO(cretz): Or better yet, just rework this code
			var importCfgFile string
			for i, arg := range s.origCLIArgs {
				if arg == "-importcfg" {
					importCfgFile = s.origCLIArgs[i+1]
					break
				}
			}
			if importCfgFile == "" {
				return nil, fmt.Errorf("no import cfg file for link")
			}
			importCfg, err := s.loadImportCfg(importCfgFile)
			if err != nil {
				return nil, fmt.Errorf("failed loading link import cfg: %w", err)
			}
			for _, line := range importCfg.lines {
				if !strings.HasPrefix(line, "packagefile ") {
					continue
				}
				pkgPath := strings.TrimPrefix(line[:strings.Index(line, "=")], "packagefile ")
				args = append(args, pkgPath)
			}
		}

		s.Debugf("Getting dependent package action IDs via go command with args %v", args)
		cmd := exec.Command("go", args...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("failed listing packages: %w. Output: %s", err, b)
		}
		// Go over each line, breaking out the action ID and the package path
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		pkgActionIDs := make(map[string][]byte, len(lines))
		for _, line := range lines {
			lastPipe := strings.LastIndex(line, "|")
			if lastPipe < 0 {
				return nil, fmt.Errorf("invalid list line: %v", line)
			}
			afterPipeSlash := strings.Index(line[lastPipe:], "/")
			// If there is no slash, there is no action ID
			if afterPipeSlash < 0 {
				continue
			}
			afterPipeSlash += lastPipe
			pkgActionID, err := base64.RawURLEncoding.DecodeString(line[lastPipe+1 : afterPipeSlash])
			if err != nil {
				return nil, fmt.Errorf("invalid action ID: %w, list line: %v", err, line)
			}
			pkgPath := line[:lastPipe]
			// The pkg path may be in the form of "foo [foo.test]", so we must remove
			// the bracketed part
			spaceIndex := strings.Index(pkgPath, " ")
			if spaceIndex > 0 {
				if !strings.HasSuffix(pkgPath, ".test]") {
					return nil, fmt.Errorf("assuming test because space in package path, but got %v", pkgPath)
				}
				pkgPath = pkgPath[:spaceIndex]
			}
			pkgActionIDs[pkgPath] = pkgActionID
		}
		s._depPkgActionIDs = pkgActionIDs
	}
	return s._depPkgActionIDs, nil
}

func (s *Superpose) toolexecVersionFull(tool string, args []string) error {
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
	s.hash.Reset()
	s.hash.Write(goToolID)
	s.hash.Write([]byte("/superpose/"))
	s.hash.Write(exeContentID)
	s.hash.Write([]byte("/"))
	s.hash.Write([]byte(s.Config.Version))
	// Go only allows a certain size
	contentID := base64.RawURLEncoding.EncodeToString(s.hash.Sum(nil)[:15])

	// Append content ID as end of fake build ID
	fmt.Printf("%s +superpose buildID=_/_/_/%s\n", goOutLine, contentID)
	return nil
}

type compileFlags struct {
	// This set includes the compile executable as first arg
	args                                                               []string
	outputIndex, trimPathIndex, pkgIndex, buildIDIndex, importCfgIndex int
	goFileIndexes                                                      map[string]int
}

func (c *compileFlags) parse(args []string) error {
	// TODO(cretz): This is brittle because it assumes these flags don't use "="
	// form which is only based on observation
	c.args = args
	c.goFileIndexes = map[string]int{}
	for i, arg := range args {
		switch arg {
		case "-o":
			c.outputIndex = i + 1
		case "-trimpath":
			c.trimPathIndex = i + 1
		case "-p":
			c.pkgIndex = i + 1
		case "-buildid":
			c.buildIDIndex = i + 1
		case "-importcfg":
			c.importCfgIndex = i + 1
		default:
			// Even if not a file but happens to have this suffix, harmless to store
			// in map anyways
			if strings.HasSuffix(arg, ".go") {
				c.goFileIndexes[arg] = i
			}
		}
	}
	// Confirm all present
	switch {
	case c.outputIndex == 0:
		return fmt.Errorf("missing -o")
	case c.trimPathIndex == 0:
		return fmt.Errorf("missing -trimpath")
	case c.pkgIndex == 0:
		return fmt.Errorf("missing -p")
	case c.buildIDIndex == 0:
		return fmt.Errorf("missing -buildid")
	case c.importCfgIndex == 0:
		return fmt.Errorf("missing -importcfg")
	}
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
