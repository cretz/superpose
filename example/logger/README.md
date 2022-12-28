# Altered Logger

This example shows that in a different dimension you can alter the standard library. Specifically we change "Hello" to
"Aloha" for any logs in the other dimension.

## Compiling

To compile, first the compiler tool must be compiled. From the root of the repo, run:

    go build ./example/logger/superpose-alterlog

Now it can be executed as toolexec, for example:

    go run -toolexec /path/to/superpose-alterlog ./example/logger

Note how the output of the first print is "Hello, World!" but the second is "Aloha, World!".

It can also be tested using that same tool:

    go test -toolexec /path/to/superpose-alterlog ./example/logger/superpose-alterlog