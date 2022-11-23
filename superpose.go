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
)

type Config struct {
	// Required, and must be unique for each transformer change (this affects
	// cache)
	Version string
	// Keyed by dimension
	Transformers  map[string]Transformer
	Verbose       bool
	RetainTempDir bool
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
		// TODO(cretz):
		// var err error
		// if goToolArgs, err = s.transformCompileArgs(goToolArgs); err != nil {
		// 	return err
		// }
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

func (s *Superpose) transformCompileArgs(args []string) ([]string, error) {
	// TODO(cretz):
	// * For every dimension, run entire transformer on every package and
	//   dependency package
	//   * Each package transformed should be at the same full import path but one
	//     deeper based on dimension
	//   * Also make sure to replace all imports
	//   * Consider loading without NeedDeps
	//   * Can we reuse go mod area or go cache area or gopath area for our stuff?
	// * If any of the compiled files have a //<dimension>:<func>|"<in>" then
	//   create a new file populating those vars on init
	//   * Make it clear dimensions not reference-able within each other currently
	//   * Can be a temp file, only needs to last for compile
	panic("TODO")
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
	exeContentID, err := loadExeContentID()
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

func loadExeContentID() ([]byte, error) {
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
	return base64.RawURLEncoding.DecodeString(contentID)
}
