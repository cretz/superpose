package main

import (
	"os"
	"runtime"
	"testing"

	// We intentionally don't run goimports on this so that we don't get the
	// package alias
	"github.com/cretz/superpose/tests/simple/diffpkgname"
	"github.com/stretchr/testify/require"
)

func DiffPkgReturnString() string {
	return notsamepkgname.ReturnString()
}

var OtherDiffPkgReturnString func() string //tests-simple:DiffPkgReturnString

func TestDifferentlyNamedPackage(t *testing.T) {
	// Let's read our own source to confirm that the import did not get an alias
	// by someone accidentally formatting it
	_, currFile, _, _ := runtime.Caller(0)
	currFileBytes, err := os.ReadFile(currFile)
	require.NoError(t, err)
	require.Contains(t, string(currFileBytes), "\t\"github.com/cretz/superpose/tests/simple/diffpkgname\"",
		"source formatting may have added alias for import")

	// Now run the test
	require.Equal(t, "diff pkg string", DiffPkgReturnString())
	require.Equal(t, "foo", OtherDiffPkgReturnString())
}
