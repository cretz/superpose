package goembed

import _ "embed"

//go:embed string.txt
var s string

func ReturnString() string { return s }

func ReturnUnchangedString() string { return s }
