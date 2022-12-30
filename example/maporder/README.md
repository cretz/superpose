# Map Order

This example shows a superpose-based compiler with deterministic map iteration.

## Compiling

To compile, first the compiler tool must be compiled. From the root of the repo, run:

    go build ./example/maporder/superpose-maporder

Now it can be executed as toolexec, for example:

    go run -toolexec /path/to/superpose-maporder ./example/maporder

Note how the output of the second map print is deterministically sorted each time.

TODO(cretz): Insertion-based ordering for maps