package main

import (
	"io"
	"log"
	"strings"
	"testing"
)

// We have to return an interface here instead of `*log.Logger` because in the
// other dimension, "log" is a different package.
func NewLogger(w io.Writer) interface{ Print(...any) } { return log.New(w, "", 0) }

var NewAlteredLogger func(w io.Writer) interface{ Print(...any) } //alterlog:NewLogger

func TestTransformer(t *testing.T) {
	var out strings.Builder
	NewLogger(&out).Print("Hi, Hello")
	if out := strings.TrimSpace(out.String()); out != "Hi, Hello" {
		t.Fatalf("invalid results of %v", out)
	}
	out.Reset()
	NewAlteredLogger(&out).Print("Hi, Hello")
	if out := strings.TrimSpace(out.String()); out != "Hi, Aloha" {
		t.Fatalf("invalid results of %v", out)
	}
}
