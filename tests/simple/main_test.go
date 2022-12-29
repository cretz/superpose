package main

import (
	"testing"

	"github.com/cretz/superpose/tests/simple/buildtag"
	importalias "github.com/cretz/superpose/tests/simple/diffpkgname"
	"github.com/cretz/superpose/tests/simple/goembed"
	"github.com/stretchr/testify/require"
)

func ReturnString() string { return "some string" }

var OtherReturnString func() string //tests-simple:ReturnString

func TestSimple(t *testing.T) {
	require.Equal(t, "some string", ReturnString())
	require.Equal(t, "foo", OtherReturnString())
}

func ImportAliasReturnString() string { return importalias.ReturnString() }

var OtherImportAliasReturnString func() string //tests-simple:ImportAliasReturnString

func TestImportAlias(t *testing.T) {
	require.Equal(t, "diff pkg string", ImportAliasReturnString())
	require.Equal(t, "foo", OtherImportAliasReturnString())
}

func BuildTagReturnString() string { return buildtag.ReturnString() }

var OtherBuildTagAliasReturnString func() string //tests-simple:BuildTagReturnString

func GoEmbedReturnString() string { return goembed.ReturnString() }

var OtherGoEmbedReturnString func() string //tests-simple:GoEmbedReturnString

func GoEmbedReturnUnchangedString() string { return goembed.ReturnUnchangedString() }

var OtherGoEmbedReturnUnchangedString func() string //tests-simple:GoEmbedReturnUnchangedString

func TestGoEmbed(t *testing.T) {
	require.Equal(t, "embedded string", GoEmbedReturnString())
	require.Equal(t, "foo", OtherGoEmbedReturnString())
	require.Equal(t, "embedded string", GoEmbedReturnUnchangedString())
	require.Equal(t, "embedded string", OtherGoEmbedReturnUnchangedString())
}
