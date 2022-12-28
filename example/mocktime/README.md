# Altered Logger

This example shows that in a different dimension you can alter `time.Now()`. This is a very basic approach, but a more
robust time mocking solution can be built like this.

## Compiling

To compile, first the compiler tool must be compiled. From the root of the repo, run:

    go build ./example/mocktime/superpose-mocktime

Now it can be executed as toolexec, for example:

    go run -toolexec /path/to/superpose-mocktime ./example/mocktime

Note how the log statement in the mocked environment shows whatever time we set.