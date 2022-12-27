package superposetest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
)

// TODO(cretz): Document that T must be JSON serializable
type BuildTransformedExeConfig[T any] struct {
	TransformerExe     string
	RunFunc            func() (T, error)
	RunFuncExprCall    string
	RunFuncExprPackage string
	Verbosef           func(string, ...any)
}

type TransformedExe[T any] struct {
	Exe      string
	Verbosef func(string, ...any)
}

// TODO(cretz): Document that caller is required to remove file after done
func BuildTransformedExe[T any](
	ctx context.Context,
	config BuildTransformedExeConfig[T],
) (*TransformedExe[T], error) {
	if config.TransformerExe == "" {
		return nil, fmt.Errorf("transformer exe required")
	}
	// Prepare expr and imports for run func
	if config.RunFunc != nil {
		if config.RunFuncExprCall != "" || config.RunFuncExprPackage != "" {
			return nil, fmt.Errorf("cannot have create run expr call/package if func is given")
		}
		name, pkg, err := funcNameAndPackage(reflect.ValueOf(config.RunFunc))
		if err != nil {
			return nil, err
		}
		config.RunFuncExprCall = name + "()"
		config.RunFuncExprPackage = pkg
	} else if config.RunFuncExprCall == "" || config.RunFuncExprPackage == "" {
		// Should be no reason to have an expr without a package
		return nil, fmt.Errorf("must have run func or expr call/package")
	}

	// Find the go.mod for the package
	goModPath, err := goModFileForPackage(ctx, config.RunFuncExprPackage)
	if err != nil {
		return nil, err
	}

	// Create the code which just JSON encodes successful result
	code := `package main

import (
	"encoding/json"
	"fmt"
	"os"

	__run ` + strconv.Quote(config.RunFuncExprPackage) + `
)
	
func main() {
	res, err := __run.` + config.RunFuncExprCall + `
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	} else if b, err := json.Marshal(res); err != nil {
		fmt.Println(err)
		os.Exit(1)
	} else {
		fmt.Println(string(b))
	}
}
`

	// Put in a temp file we will remove at the end
	codeFile, err := os.CreateTemp("", "*-superpose-test-run.go")
	if err != nil {
		return nil, err
	}
	if config.Verbosef != nil {
		config.Verbosef("Writing code to %v:\n%v\n", codeFile.Name(), code)
	}
	defer os.Remove(codeFile.Name())
	_, err = codeFile.Write([]byte(code))
	if closeErr := codeFile.Close(); err != nil {
		return nil, err
	} else if closeErr != nil {
		return nil, closeErr
	}

	// Build the file in a temp location we will _not_ remove at the end
	exePath := tempExePlaceholder("-superpose-test-run")
	args := []string{"go", "build", "-modfile", goModPath, "-o", exePath,
		"-toolexec", config.TransformerExe, codeFile.Name()}
	if config.Verbosef != nil {
		config.Verbosef("Running %v", args)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// It is important that we run in the same directory as the go.mod file so
	// that the "go list" inside compile works properly
	cmd.Dir = filepath.Dir(goModPath)
	out, err := cmd.CombinedOutput()
	if config.Verbosef != nil {
		config.Verbosef("Output:\n%s\n", out)
	}
	if err != nil {
		return nil, fmt.Errorf("failed building exe, error: %w, output: %s", err, out)
	}
	return &TransformedExe[T]{Exe: exePath, Verbosef: config.Verbosef}, nil
}

func (t *TransformedExe[T]) Run(ctx context.Context) (T, error) {
	var ret T
	if t.Verbosef != nil {
		t.Verbosef("Running %v", t.Exe)
	}
	out, err := exec.CommandContext(ctx, t.Exe).CombinedOutput()
	if t.Verbosef != nil {
		t.Verbosef("Output:\n%s\n", out)
	}
	if err != nil {
		return ret, fmt.Errorf("failed running, error: %w, output: %s", err, out)
	} else if err := json.Unmarshal(bytes.TrimSpace(out), &ret); err != nil {
		return ret, fmt.Errorf("failed unmarshalling result, error: %w, output: %s", err, out)
	}
	return ret, nil
}
