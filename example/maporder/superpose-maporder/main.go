package main

import (
	"github.com/cretz/superpose"
	"golang.org/x/tools/go/packages"
)

func main() {
	superpose.RunMain(
		superpose.Config{Version: "1", Dimensions: []string{"mapsort"}},
		superpose.RunMainConfig{AssumeToolexec: true},
	)
}

type transformer struct{}

func (transformer) AppliesToPackage(ctx *superpose.TransformContext, pkg *packages.Package) (bool, error) {
	// Does not apply to "runtime" our ourselves
	panic("TODO")
}

func (transformer) Transform(ctx *superpose.TransformContext, pkgs []*superpose.TransformPackage) error {
	panic("TODO")
}
