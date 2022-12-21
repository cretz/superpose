package superpose

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rogpeppe/go-internal/cache"
)

type Config struct {
	// Required, and must be unique for each transformer change (this affects
	// cache)
	Version string
	// Keyed by dimension
	Transformers  map[string]Transformer
	Verbose       bool
	RetainTempDir bool
	// By default this is <user-cache>/superpose-build
	BuildCacheDir string

	// TODO(cretz): Allow customizing of load mode. If NeedDeps is present, import
	// packages could be reused instead of running load per package.
	// LoadMode: packages.LoadMode
}

type Superpose struct {
	Config  Config
	tempDir string
	// Only properly set after we know we're at the compile step
	flags compileFlags
	// Lazy, use buildCache()
	_buildCache *cache.Cache
	// Lazy, use pkgActionIDs()
	_pkgActionIDs map[string]cache.ActionID
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
	// Cleanup the cache on complete if it's present (meaning it was used)
	defer func() {
		if s._buildCache != nil {
			s._buildCache.Trim()
		}
	}()

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
	_, tool := filepath.Split(args[0])
	if runtime.GOOS == "windows" {
		tool = strings.TrimSuffix(tool, ".exe")
	}

	// Go uses -V=full at first, so handle just that
	if len(args) == 2 && args[1] == "-V=full" {
		return s.toolexecVersionFull(tool, args)
	}

	s.Debugf("Intercepting toolexec with args: %v", args)
	goToolArgs := args[1:]
	if tool == "compile" {
		var err error
		if goToolArgs, err = s.onCompile(goToolArgs); err != nil {
			return err
		}
		s.Debugf("Updated compile args to %v", goToolArgs)
	} else {
		s.Debugf("No interception needed for tool %v", tool)
	}

	// Run the command
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

func (s *Superpose) DimensionPackagePath(origPkg string, dimension string) string {
	// Just delimit with two underscores for now
	return origPkg + "__" + dimension
}

func (s *Superpose) dimensionPackageActionID(origPkg string, dim string) (cache.ActionID, error) {
	// Get the original package action ID and make a subkey
	pkgActionIDs, err := s.pkgActionIDs()
	pkgActionID, ok := pkgActionIDs[origPkg]
	if err != nil {
		return pkgActionID, err
	} else if !ok {
		return pkgActionID, fmt.Errorf("unable to find action ID for package %v", origPkg)
	}
	return cache.Subkey(pkgActionID, "superpose-dimension: "+dim), nil
}

// Errors or gives string file, never empty string with no error
func (s *Superpose) dimensionPackageFile(origPkg string, dim string) (string, error) {
	actionID, err := s.dimensionPackageActionID(origPkg, dim)
	if err != nil {
		return "", err
	}
	cache, err := s.buildCache()
	if err != nil {
		return "", err
	}
	file, _, err := cache.GetFile(actionID)
	if err != nil {
		return "", fmt.Errorf("failed getting action ID for pkg %v in dimension %v: %w", origPkg, dim, err)
	}
	return file, nil
}

func (s *Superpose) buildCache() (*cache.Cache, error) {
	if s._buildCache == nil {
		var err error
		if s._buildCache, err = cache.Open(s.Config.BuildCacheDir); err != nil {
			return nil, fmt.Errorf("failed opening build cache at %v: %w", s.Config.BuildCacheDir, err)
		}
	}
	return s._buildCache, nil
}

func (s *Superpose) onCompile(args []string) (newArgs []string, err error) {
	// Parse flags
	if err := s.flags.parse(args); err != nil {
		return nil, err
	}

	// Compile dimensions
	if err := s.compileDimensions(); err != nil {
		return nil, err
	}

	// Add bridge file to args if any. If no bridge file, just reuse the same
	// args.
	bridgeFile, err := s.buildBridgeFile()
	if bridgeFile == nil || err != nil {
		return args, err
	}

	// Now that we know there is a bridge file, copy all the args and add bridge
	// file to the end
	newArgs = make([]string, len(args)+1)
	copy(newArgs, args)
	newArgs[len(newArgs)-1] = bridgeFile.fileName

	// Also, make a temporary import config with any additional dimension refs
	newArgs[s.flags.importCfgIndex], err = s.newTempImportCfg(bridgeFile.dimPkgRefs, true)
	if err != nil {
		return nil, fmt.Errorf("failed creating bridge importcfg: %w", err)
	}

	// Note, we don't have to alter the build ID for the compile args because the
	// build ID already takes into account the global build ID with our version
	// from -V=full.
	return newArgs, nil
}

func (s *Superpose) pkgActionIDs() (map[string]cache.ActionID, error) {
	if s._pkgActionIDs == nil {
		// Use "go list" to get action IDs for this package and every dependency
		b, err := exec.Command(
			"go", "list", "-f", "{{.ImportPath}}|{{.BuildID}}", "-export", "-deps", s.pkgPath()).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("failed listing packages: %w. Output: %s", err, b)
		}
		// Go over each line, breaking out the action ID and the package path
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		s._pkgActionIDs = make(map[string]cache.ActionID, len(lines))
		for _, line := range lines {
			lastPipe := strings.LastIndex(line, "|")
			lastSlash := strings.LastIndex(line, "/")
			if lastPipe < 0 {
				return nil, fmt.Errorf("invalid list line: %v", line)
			}
			// If there is no slash, there is no action ID
			if lastSlash < 0 {
				continue
			}
			var pkgActionID cache.ActionID
			pkgActionIDSlice, err := base64.RawURLEncoding.DecodeString(line[lastSlash+1:])
			if err != nil {
				return nil, fmt.Errorf("invalid action ID: %w, list line: %v", err, line)
			} else if len(pkgActionIDSlice) != len(pkgActionID) {
				return nil, fmt.Errorf("expected action ID len %v, got %v", len(pkgActionID), len(pkgActionIDSlice))
			}
			copy(pkgActionID[:], pkgActionIDSlice)
			s._pkgActionIDs[line[:lastPipe]] = pkgActionID
		}
		// Do a sanity check to confirm this package's action ID matches build ID
		buildID := s.flags.args[s.flags.buildIDIndex]
		if buildIDSlashIndex := strings.Index(buildID, "/"); buildIDSlashIndex < 0 {
			return nil, fmt.Errorf("invalid build ID of %v", buildID)
		} else if actionID, err := base64.RawURLEncoding.DecodeString(buildID[:buildIDSlashIndex]); err != nil {
			return nil, fmt.Errorf("failed decoding action ID: %w", err)
		} else if v := s._pkgActionIDs[s.pkgPath()]; !bytes.Equal(actionID, v[:]) {
			return nil, fmt.Errorf("expected action ID %v, but package action ID showed as %v", actionID, v)
		}
	}
	return s._pkgActionIDs, nil
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

func (s *Superpose) pkgPath() string {
	return s.flags.args[s.flags.pkgIndex]
}

type dimPkgRef struct {
	dim         string
	origPkgPath string
}

// If not appendOnly, replaces
func (s *Superpose) newTempImportCfg(dimPkgs map[dimPkgRef]struct{}, appendOnly bool) (string, error) {
	// Read existing
	origImportCfgFile := s.flags.args[s.flags.importCfgIndex]
	importCfgBytes, err := os.ReadFile(origImportCfgFile)
	if err != nil {
		return "", fmt.Errorf("failed reading importcfg file: %w", err)
	}
	importCfgLines := strings.Split(strings.TrimSpace(string(importCfgBytes)), "\n")

	// If not append, remove any ones that currently match
	if !appendOnly {
		n := 0
		for _, line := range importCfgLines {
			// Only keep if not in dimPkgs
			keep := true
			if strings.HasPrefix(line, "packagefile ") {
				pkgPath := line[strings.Index(line, " ")+1 : strings.Index(line, "=")]
				for dimPkg := range dimPkgs {
					if dimPkg.origPkgPath == pkgPath {
						keep = false
						break
					}
				}
			}
			if keep {
				importCfgLines[n] = line
				n++
			}
		}
		importCfgLines = importCfgLines[:n]
	}

	// Add all references
	for dimPkg := range dimPkgs {
		pkgFile, err := s.dimensionPackageFile(dimPkg.origPkgPath, dimPkg.dim)
		if err != nil {
			return "", err
		}
		importCfgLines = append(importCfgLines,
			fmt.Sprintf("packagefile %v=%v", s.DimensionPackagePath(dimPkg.origPkgPath, dimPkg.dim), pkgFile))
	}

	// Create new temp importcfg
	tmpDir, err := s.UseTempDir()
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(tmpDir, "importcfg")
	if err != nil {
		return "", err
	}
	defer f.Close()
	// Write lines
	s.Debugf("Copying importcfg from %v to %v and setting dimension packages (append? %v): %#v",
		origImportCfgFile, f.Name(), appendOnly, dimPkgs)
	_, err = f.Write([]byte(strings.Join(importCfgLines, "\n") + "\n"))
	if err != nil {
		return "", err
	}
	return f.Name(), nil
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
	// Sanity check the package path
	if pkgPath := os.Getenv("TOOLEXEC_IMPORTPATH"); pkgPath == "" {
		return fmt.Errorf("no import path found")
	} else if pkgPath != args[c.pkgIndex] {
		// Sanity check
		return fmt.Errorf("import path is %v but package arg is %v", pkgPath, args[c.pkgIndex])
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
