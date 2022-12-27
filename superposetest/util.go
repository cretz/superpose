package superposetest

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

func funcNameAndPackage(fn reflect.Value) (name string, pkg string, err error) {
	if fn.Kind() != reflect.Func {
		return "", "", fmt.Errorf("not a function")
	}
	fullName := runtime.FuncForPC(fn.Pointer()).Name()
	if fullName == "" {
		return "", "", fmt.Errorf("no runtime function")
	}
	lastDot := strings.LastIndex(fullName, ".")
	return fullName[lastDot+1:], fullName[:lastDot], nil
}

func goModFileForPackage(ctx context.Context, pkgPath string) (string, error) {
	out, err := exec.CommandContext(ctx, "go", "list", "-f", "{{.Module.GoMod}}", pkgPath).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed finding go.mod for %v, error: %w, output: %s", pkgPath, err, out)
	}
	goModPath := strings.TrimSpace(string(out))
	if goModPath == "" {
		return "", fmt.Errorf("no go.mod found for %v", pkgPath)
	}
	return goModPath, nil
}

var noPaddingBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func tempExePlaceholder(suffix string) string {
	randBytes := make([]byte, 15)
	if _, err := rand.Read(randBytes); err != nil {
		panic(err)
	}
	name := filepath.Join(os.TempDir(), strings.ToLower(noPaddingBase32.EncodeToString(randBytes))+suffix)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}
