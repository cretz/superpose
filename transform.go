package superpose

import (
	"context"

	"golang.org/x/tools/go/packages"
)

type TransformContext struct {
	context.Context
	Dimension string
}

type TransformPackage struct {
	*packages.Package
	OrigBuildID        string
	LastBuiltVersion   string
	CurrentlyCompiling bool
	Transformed        bool
}

type Transformer interface {
	AppliesToPackage(ctx *TransformContext, pkg *packages.Package) (bool, error)
	Transform(ctx *TransformContext, pkgs []*TransformPackage) error
}
