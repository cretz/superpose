package main

import (
	"context"

	"github.com/cretz/superpose"
)

func main() {
	superpose.RunMain(
		context.Background(),
		superpose.Config{
			Version: superpose.MustLoadCurrentExeContentID(),
			Transformers: map[string]superpose.Transformer{
				// Transform both of these dimensions
				"maporder_sorted": transformerSorted{},
				// TODO(cretz): "mapsort_insertion": transformerInsertion{},
			},
			// Set to true to see compilation details
			Verbose: false,
		},
		superpose.RunMainConfig{},
	)
}

const (
	mapIterPkg   = "github.com/cretz/superpose/example/maporder/superpose-maporder/mapiter"
	mapIterAlias = "__mapiter"
)
