# Map Order

This example shows a superpose-based compiler with two dimensions for deterministic map iteration.

## Compiling

To compile, first the compiler tool must be compiled. From the root of the repo, run:

    go build ./example/maporder/superpose-maporder

Now it can be executed as toolexec, for example:

    go run ./example/maporder -toolexec superpose-maporder
