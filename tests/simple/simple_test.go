package simple_test

import (
	"testing"

	"github.com/cretz/superpose/superposetest"
	"github.com/cretz/superpose/tests/simple"
	"github.com/stretchr/testify/require"
)

func TestSimple(t *testing.T) {
	env := superposetest.NewEnv(t, simple.Dimension, simple.NewTransformer)
	res := superposetest.Run(env, simple.Result)
	require.Equal(t, []string{"string: foo, dimension: <main>", "string: bar, dimension: simple-dim"}, res)
}
