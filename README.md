# Superpose [![PkgGoDev](https://pkg.go.dev/badge/github.com/cretz/superpose)](https://pkg.go.dev/github.com/cretz/superpose)

Superpose is a library for creating Go compiler wrappers/plugins that support transforming packages in other
"dimensions" and making them callable from the original package.

Quick start example from the [example/mocktime](example/mocktime) README, can build the tool:

    go build ./example/mocktime/superpose-mocktime

Then can be executed as `toolexec` with the just-built executable:

    go run -toolexec /path/to/superpose-mocktime ./example/mocktime

Note how there are log statements _outside_ the dimension with _normal_ timestamps and _inside_ the dimension with
_mocked_ timestamps. This is because `time.Now()` is altered in the separate dimension and therefore all things that
reference `time` in that dimension are altered too, e.g. the `log` package.

WARNING: This library is an intentionally-untagged proof of concept with no guarantees on future maintenance. Many
advanced uses may not be supported.

## Overview

This library leverages the `-toolexec` option of `go` `build`/`run`/`test` to intercept compilation and allow
transforming certain packages in a separate dimension that are compiled alongside the untransformed code. Then a
"bridge" method can call into the other dimension. Developers simply have to write the transformer and most details
concerning caching, building, and other nuances are taken care of.

The uses of this are the same as any other compile-time transformer. Potential uses:

* Mocking things like current time and time movement
* Compile-time macros and code generation
* Aspect oriented use cases like injecting log info
* "Sandboxing" and other runtime call restrictions (albeit not secure)
* External manipulation of third party or standard library packages
* Tooling support (e.g. how `-race`, code coverage, `go:embed`, etc can work)

Granted, as with all tools like this and especially in the Go ecosystem, compile-time code transformation should be the
last resort. It should only be used when it's really needed. It can also be a bit unwieldy for the compiling developer
as they have to opt-in with a special argument.

## Examples

* [example/logger](example/logger) - Shows replacing standard library code by replacing "Hello" with "Aloha" in all logs
  when running under the other dimension. Also shows a test case.
* [example/maporder](example/maporder) - More advanced example showing how to have deterministic map iteration
* [example/mocktime](example/mocktime) - Shows a basic way to replace `time.Now()` for a mock clock

See the README in each example for how to run it.

## Usage

### Creating a transformer

TODO(cretz):

### Using a transformer

TODO(cretz):
* Build tags

### Referencing another dimension

TODO(cretz):

### Knowing you're in a dimension

TODO(cretz):

### Testing

TODO(cretz):

### Advanced 

TODO(cretz):
* Patch details
* Additional package includes
* Cache busting/invalidation
* Line directives
* Additional flags
* Debugging

## How it Works

TODO(cretz):
* Background on how `go build` _really_ works behind the scenes
* What the superpose `-toolexec` command does
  * On `-V=full`
  * On `compile`
    * Compile each dimension
      * Loading packages
      * Run transformer
      * Patch imports
      * Patch bool vars
      * Patch line directives
      * Compile with importcfg mutated
    * Build bridge file
      * Init statements
      * importcfg mutated
  * On `link`
    * importcfg mutated

## Caveats

TODO(cretz):
* reflect.Type.PkgPath() is the dimension package
  * But even that can be patched if it must be
* Perf and mem size
* Crossing the boundary

## Why

TODO(cretz):