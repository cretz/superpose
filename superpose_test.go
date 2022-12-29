package superpose_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type test struct {
	dir       string
	buildTags []string
}

var tests = []test{
	{dir: "simple"},
	{dir: "simple", buildTags: []string{"some_build_tag"}},
}

func TestSuperpose(t *testing.T) {
	// Keep count for name disambiguity
	testDirCount := map[string]int{}
	for _, test := range tests {
		name := test.dir
		if testDirCount[test.dir] = testDirCount[test.dir] + 1; testDirCount[test.dir] > 1 {
			name += "#" + strconv.Itoa(testDirCount[test.dir])
		}
		t.Run(name, test.run)
	}
}

var currDir string

func init() {
	_, currFile, _, _ := runtime.Caller(0)
	currDir = filepath.Dir(currFile)
}

func (test *test) run(t *testing.T) {
	// Mark it as parallel
	t.Parallel()

	absRootTestDir := filepath.Join(currDir, "tests")
	absTestDir := filepath.Join(absRootTestDir, test.dir)

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
	toolexec := transformerExe
	if len(test.buildTags) > 0 {
		toolexec += " -buildtags " + strings.Join(test.buildTags, ",")
	}
	args = []string{"test", "-toolexec", toolexec}
	if testing.Verbose() {
		args = append(args, "-v")
	}
	if len(test.buildTags) > 0 {
		args = append(args, "-tags", strings.Join(test.buildTags, ","))
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
