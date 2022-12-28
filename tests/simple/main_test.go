package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func ReturnString() string { return "some string" }

var OtherReturnString func() string //tests-simple:ReturnString

func TestSimple(t *testing.T) {
	require.Equal(t, "some string", ReturnString())
	require.Equal(t, "foo", OtherReturnString())
}
