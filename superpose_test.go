package superpose_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

var testDirs = []string{
	"simple",
}

func TestSuperpose(t *testing.T) {
	for _, testDir := range testDirs {
		testDir := testDir
		t.Run(testDir, func(t *testing.T) { runGoTest(t, testDir) })
	}
}

var currDir string

func init() {
	_, currFile, _, _ := runtime.Caller(0)
	currDir = filepath.Dir(currFile)
}

func runGoTest(t *testing.T, testDir string) {
	// Mark it as parallel
	t.Parallel()

	absRootTestDir := filepath.Join(currDir, "tests")
	absTestDir := filepath.Join(absRootTestDir, testDir)

	// Compile the transformer to a temporary location
	transformerExe := filepath.Join(t.TempDir(), "transformer")
	if runtime.GOOS == "windows" {
		transformerExe += ".exe"
	}
	args := []string{"build", "-o", transformerExe}
	t.Logf("Running go with args %v at %v", args, absTestDir)
	cmd := exec.Command("go", args...)
	cmd.Dir = absTestDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed building transformer: %v, output:\n----\n%s\n----", err, out)
	}

	// Run Go test, passing "-v" if it was set
	args = []string{"test", "-toolexec", transformerExe}
	if testing.Verbose() {
		args = append(args, "-v")
	}
	t.Logf("Running go with args %v at %v", args, absTestDir)
	cmd = exec.Command("go", args...)
	cmd.Dir = absTestDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Sub test failed: %v, output:\n----\n%s\n----", err, out)
	} else {
		t.Logf("Go test output:\n----\n%s\n----", out)
	}
}
