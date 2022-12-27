package superposetest

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/cretz/superpose"
)

type Env struct {
	T *testing.T

	Dimension      string
	CreateFunc     func() superpose.Transformer
	DisableVerbose bool
	// Leave this blank to use default randomly generated
	FixedVersion string

	transformerExe     string
	transformerExeLock sync.Mutex
}

func NewEnv(t *testing.T, dimension string, createFunc func() superpose.Transformer) *Env {
	return &Env{T: t, Dimension: dimension, CreateFunc: createFunc}
}

func Run[T any](env *Env, runFunc func() (T, error)) T {
	if env.T == nil {
		panic("missing testing.T")
	}

	// Build transformer
	env.transformerExeLock.Lock()
	var err error
	if env.transformerExe == "" {
		buildConfig := BuildTransformerExeConfig{
			Dimension:    env.Dimension,
			CreateFunc:   env.CreateFunc,
			FixedVersion: env.FixedVersion,
		}
		if testing.Verbose() && !env.DisableVerbose {
			buildConfig.Verbosef = env.T.Logf
		}
		env.transformerExe, err = BuildTransformerExe(context.Background(), buildConfig)
		if err == nil {
			env.T.Cleanup(func() { os.Remove(env.transformerExe) })
		}
	}
	env.transformerExeLock.Unlock()
	if err != nil {
		env.T.Fatalf("Failed building transformer: %v", err)
	}

	// Build exe and then run
	buildConfig := BuildTransformedExeConfig[T]{
		TransformerExe: env.transformerExe,
		RunFunc:        runFunc,
	}
	if testing.Verbose() && !env.DisableVerbose {
		buildConfig.Verbosef = env.T.Logf
	}
	exe, err := BuildTransformedExe(context.Background(), buildConfig)
	if err != nil {
		env.T.Fatalf("Failed building transformed exe: %v", err)
	}
	defer os.Remove(exe.Exe)
	ret, err := exe.Run(context.Background())
	if err != nil {
		env.T.Fatalf("Failed running transformed exe: %v", err)
	}
	return ret
}
