//go:build some_build_tag

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildTag(t *testing.T) {
	require.Equal(t, "build tag on", BuildTagReturnString())
	require.Equal(t, "foo", OtherBuildTagAliasReturnString())
}
