package superposetest

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"

	"github.com/cretz/superpose"
)

type BuildTransformerExeConfig struct {
	Dimension             string
	CreateFunc            func() superpose.Transformer
	CreateFuncExprCall    string
	CreateFuncExprPackage string
	CreateFuncExprFile    string
	Verbosef              func(string, ...any)
	// Leave this blank to use default randomly generated
	FixedVersion string
}

// TODO(cretz): Document that caller is required to remove file after done
func BuildTransformerExe(
	ctx context.Context,
	config BuildTransformerExeConfig,
) (string, error) {
	if config.Dimension == "" {
		return "", fmt.Errorf("dimension required")
	}
	// Prepare expr and imports for creating transformer
	if config.CreateFunc != nil {
		if config.CreateFuncExprCall != "" || config.CreateFuncExprPackage != "" {
			return "", fmt.Errorf("cannot have create expr call/package if func is given")
		}
		name, pkg, err := funcNameAndPackage(reflect.ValueOf(config.CreateFunc))
		if err != nil {
			return "", err
		}
		config.CreateFuncExprCall = name + "()"
		config.CreateFuncExprPackage = pkg
	} else if config.CreateFuncExprCall == "" || config.CreateFuncExprPackage == "" {
		// Should be no reason to have an expr without a package
		return "", fmt.Errorf("must have create func or expr call/package")
	}

	// Find the go.mod for the package
	goModPath, err := goModFileForPackage(ctx, config.CreateFuncExprPackage)
	if err != nil {
		return "", err
	}

	// Generate random version if none given. This will bust cache.
	if config.FixedVersion == "" {
		randBytes := make([]byte, 20)
		if _, err := rand.Read(randBytes); err != nil {
			panic(err)
		}
		config.FixedVersion = noPaddingBase32.EncodeToString(randBytes)
	}

	// Create the code
	code := `package main

import (
	"context"
	
	"github.com/cretz/superpose"
	__transformer ` + strconv.Quote(config.CreateFuncExprPackage) + `
)

func main() {
	superpose.RunMain(
		context.Background(),
		superpose.Config{
			Version: ` + strconv.Quote(config.FixedVersion) + `,
			Transformers: map[string]superpose.Transformer{
				` + strconv.Quote(config.Dimension) + `: __transformer.` + config.CreateFuncExprCall + `,
			},
			Verbose: ` + strconv.FormatBool(config.Verbosef != nil) + `,
			ForceTransform: true,
		},
		superpose.RunMainConfig{
			AssumeToolexec: true,
		},
	)
}
`

	// Put in a temp file we will remove at the end
	codeFile, err := os.CreateTemp("", "*-superpose-test-transformer.go")
	if err != nil {
		return "", err
	}
	if config.Verbosef != nil {
		config.Verbosef("Writing code to %v:\n%v\n", codeFile.Name(), code)
	}
	defer os.Remove(codeFile.Name())
	_, err = codeFile.Write([]byte(code))
	if closeErr := codeFile.Close(); err != nil {
		return "", err
	} else if closeErr != nil {
		return "", closeErr
	}

	// Build the file in a temp location we will _not_ remove at the end
	exePath := tempExePlaceholder("-superpose-test-transformer")
	args := []string{"go", "build", "-modfile", goModPath, "-o", exePath, codeFile.Name()}
	if config.Verbosef != nil {
		config.Verbosef("Running %v", args)
	}
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	if config.Verbosef != nil {
		config.Verbosef("Output:\n%s\n", out)
	}
	if err != nil {
		return "", fmt.Errorf("failed building transformer, error: %w, output: %s", err, out)
	}
	return exePath, nil
}
