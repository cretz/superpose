package simple

import (
	"fmt"

	"github.com/cretz/superpose/tests/simple/package1"
)

func Result() ([]string, error) {
	return []string{BuildString(), simpleDimBuildString()}, nil
}

var simpleDimBuildString func() string //simple-dim:BuildString
var inSimpleDim bool                   //simple-dim:<in>

func BuildString() string {
	dimension := "<main>"
	if inSimpleDim {
		dimension = Dimension
	}
	return fmt.Sprintf("string: %v, dimension: %v", package1.ReturnString(), dimension)
}
