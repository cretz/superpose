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

---

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Contents**

- [Overview](#overview)
- [Examples](#examples)
- [Usage](#usage)
  - [Creating a transformer](#creating-a-transformer)
  - [Using a transformer](#using-a-transformer)
    - [Build tags](#build-tags)
  - [Referencing another dimension](#referencing-another-dimension)
  - [Knowing we're in a dimension](#knowing-were-in-a-dimension)
  - [Testing](#testing)
  - [Advanced](#advanced)
    - [Patching](#patching)
    - [Including dependency packages during transformation](#including-dependency-packages-during-transformation)
    - [Caching](#caching)
    - [Additional flags](#additional-flags)
    - [Development and debugging](#development-and-debugging)
- [How it works in detail](#how-it-works-in-detail)
  - [High-level Go compilation primer](#high-level-go-compilation-primer)
  - [On `compile`](#on-compile)
    - [Compile dimensions](#compile-dimensions)
    - [Build bridge](#build-bridge)
  - [On `link`](#on-link)
- [Caveats](#caveats)
- [Why](#why)
- [TODO](#todo)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

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

For a basically-unusable simple example, let's say we want to change every function named "ReturnString" in our package
to return "foo".

Terms in common use:

* Bridge Function - An exported function in a file that can be used as a "bridge" call to another dimension. When a
  `var` in the _same_ file is present with a type of `func` that is the exact signature of the exported function and a
  comment in the form of `//my-dimension:MyFuncName`, it is set by the Superpose compiler to that same function in the
  other dimension.
* Dimension - A string name of a "dimension" that a transformer applies to. All packages, including applicable
  dependency packages, that are transformed for a dimension are put in mangled package paths to differentiate themselves
  from the un-transformed code.
* In-var - A `bool` `var` with a comment in the form of `//my-dimension:<in>` that Superpose sets to `true` when
  compiled in that dimension (but remains false in all other places including normal code).
* Transformer - Code for a dimension that says which packages are applied to the dimension and provides patches to files
  inside that dimension.

### Creating a transformer

We must first create a transformer. This is the main executable that is used as Go's `toolexec`, meaning it is invoked
for every Go build/compile/link/etc command. The transformer applies to a certain dimension and set of packages.
Assuming we want to use dimension name `my-dimension`, here's how it might look:

```go
package main

import (
  "context"
  "go/ast"
  "strings"

  "github.com/cretz/superpose"
)

func main() {
  superpose.RunMain(
    context.Background(),
    superpose.Config{
      // We use the current content ID of the executable of our version which
      // adds a slight performance penalty
      Version:      superpose.MustLoadCurrentExeContentID(),
      Transformers: map[string]superpose.Transformer{"my-dimension": transformer{}},
      // This is very noisy if verbose by default. Consider only setting this as
      // true during development.
      Verbose: true,
    },
    superpose.RunMainConfig{},
  )
}

type transformer struct{}

func (transformer) AppliesToPackage(ctx *superpose.TransformContext, pkgPath string) (bool, error) {
  return strings.HasPrefix(pkgPath, "example.com/mymodule"), nil
}

func (transformer) Transform(
  ctx *superpose.TransformContext,
  pkg *superpose.TransformPackage,
) (*superpose.TransformResult, error) {
  // Change any ReturnString function to return "foo"
  res := &superpose.TransformResult{
    // We set this to true so we can make sure our patched appears like it was
    // named the original file name
    AddLineDirectives: true,
    // If verbose is on, this will log the entirety of every patched file, which
    // we want during development
    LogPatchedFiles:   true,
  }
  // Go over each file in the package
  for _, file := range pkg.Syntax {
    for _, decl := range file.Decls {
      // Add patch if it's the func we want
      decl, _ := decl.(*ast.FuncDecl)
      if decl == nil || decl.Name.Name != "ReturnString" {
        continue
      }
      res.Patches = append(res.Patches, &superpose.Patch{
        // We're replacing from just after opening brace to just before closing
        // brace
        Range: superpose.Range{Pos: decl.Body.Lbrace + 1, End: decl.Body.Rbrace},
        // In addition to our return statement, we also want to set a line
        // directive before the closing brace to what it was before so all other
        // line numbers of the file still read the same
        Str: fmt.Sprintf(
          ` return "foo" /*line :%v*/`,
          pkg.Fset.Position(decl.Body.Rbrace).Line,
        ),
      })
    }
  }
  return res, nil
}
```

Not, in any package underneath `example.com/mymodule` that has a `ReturnString` top-level function, we will change it
to just return `"foo"`. A more advanced example would have done some type checking to confirm the function looked right,
but this is a simplified example.

Note how we built a patch and set `AddLineDirectives: true` and added `/*line :<line>*/` to our patch. Superpose works
on patches instead of AST alterations. This is important to retain line information. When we may alter line counts but
we want to appear in stack traces and debugger as the original line, we need `AddLineDirectives: true` to fix the
filename, and then we need to set [line directives](https://pkg.go.dev/cmd/compile#hdr-Compiler_Directives) for the
compiler.

### Using a transformer

Once that transformer is built as an executable, we can now use it in `-toolexec`. `-toolexec` build flag is accepted in
all `go` calls that may build, e.g. `go build`, `go run`, `go test`, etc. So if we had a `user_code.go` file, we
could:

    go run -toolexec /path/to/my-transformer user_code.go

#### Build tags

There is a caveat however for build tags. Go does not provide `toolexec` executables a way to know what build tags are
in use by itself and dependencies. Therefore, if we set `-tags` on the `go` command, we have to set `-buildtags` for the
`toolexec`. For example:

    go run -tags mytag -toolexec "/path/to/my-transformer -buildtags mytag" user_code.go

This ensures build tags are respected when building the other dimensions.

### Referencing another dimension

Now that we have a transformer for a dimension and know how to build with it, we need to be able to call into the
dimension. Say we have this file at `example.com/mymodule/otherpkg/return_string.go`:

```go
package otherpkg

func ReturnString() string { return "original string" }
```

Now say we want to call `otherpkg` in the other dimension. If we just call `otherpkg.ReturnString()` we'll get
`"original string"`. To call the other dimension we have to make a "bridge function".

A bridge function is an exported function in a file accompanied by a `var` of that _exact_ function signature, including
parameter/return var names, in that _same file_. The var has a special comment in the form `//my-dimension:MyFunc` that
tells the Superpose compiler that it should be set with the same function from that dimension. The package for the file
containing this bridge function must also return `true` for `Transformer.AppliesToPackage` for that dimension.

Here's an example, say at file `example.com/mymodule/cmd/main.go`, that has a bridge function to the `my-dimension`
dimension:

```go
package main

import (
  "fmt"

  "example.com/mymodule/otherpkg"
)

func CallReturnString() string { return otherpkg.ReturnString() }

var CallReturnStringInMyDimension func() string //my-dimension:CallReturnString

func main() {
  fmt.Printf("Normal code: %v\n", CallReturnString())
  fmt.Printf("Other dimension code: %v\n", CallReturnStringInMyDimension())
}
```

Running:

    go run -toolexec /path/to/my-transformer ./cmd

Will output:

    Normal code: original string
    Other dimension code: foo

Bridge functions don't have to be in the main package, they can be in any package that . Any number of bridge functions
can be defined. Since package-level vars are different in different dimensions, it may make sense to have a bridge
function reference/mutate them. Note, types from a transformed package can't be used as parameter/return to the bridge
function because it will appear as another type in the bridge and a compile error will occur.

### Knowing we're in a dimension

Sometimes in transformed code we need to know whether we're running in a dimension or not. This can be done with a
"in-var" which is a special bool `var` with a comment in the form `//my-dimension:<in>` where `<in>` is literally the
term. For example, if we had:

```go
package main

import "fmt"

var inMyDimension bool //my-dimension:<in>

func PrintSomething() {
  if inMyDimension {
    fmt.Println("In my dimension")
  } else {
    fmt.Println("In normal code")
  }
}

var PrintSomethingInMyDimension func() string //my-dimension:PrintSomething

func main() {
  PrintSomething()
  PrintSomethingInMyDimension()
}
```

Then running with the `toolexec`, the output will be:

    In normal code
    In my dimension

These in-vars can be in any transformed package and any number of them may be created. They do not have to be exported.

### Testing

An earlier incarnation of this library had an entire test framework, but it became very apparent it was much clearer to
just pass `toolexec` to `go test` too and run code and build bridge functions to test across dimensions there.

Therefore to test a transformer, just write tests with bridge functions as needed to assert the transformer did the
right thing, and run `go test` with `-toolexec` of the transformer. This means there is transformer build a step that
runs before `go test` which can be automated as needed.

### Advanced

#### Patching

`TransformResult` contains a set of patches that reference positions on the file set of the incoming package. Each
`Patch` contains a required `Range` it replaces that contains a required inclusive start `Pos` and an optional exclusive
`End`. If `End` is 0/unset, the patch will be an insertion instead of a replacement. The required `Str` of the patch
contains the string contents to patch.

Some notes about patches:

* Patches cannot overlap, so care must be taken by the transformer
* Internally, Superpose patches the package name and any transformed imports, so the transformer must make sure not to
  overlap with those patches
* If `Str` contains `{{`, it is assumed to be a Go template
  * The patch can contain `Captures` which is a named map of ranges that are made available via the `Captures` object in
    the template

Some guidance on patching:

* Patches should alter the existing code as little as possible to help preserve line counts
* `go fmt` is not applied on patched code
  * For example, many lines of code can be put on a single line separated by semicolons, which Go supports
  * A semicolon can be added after
* Using an existing AST position and adding or subtracting `1` will reference the character right after or before
  respectively
* If it is known a patch may alter line count, use a `/*line :<line>*/`-style
  [line directive](https://pkg.go.dev/cmd/compile#hdr-Compiler_Directives) afterwards to put the compiler back on the
  right line count for successive code
* In Go, it is acceptable to return early or panic early leaving dead code, so often there is no need to be concerned
  with removing code in these situations
* It is often better to immediately delegate to some proper written package for a task than to have a complicated set of
  patches (see next section)

#### Including dependency packages during transformation

When transforming, sometimes it is necessary to depend on a package that may not have been depended on by the
transformed package before. The transformer is expected to patch the `import`s necessary in source to do this. However,
the linker needs to know about any new packages to include at compile time. This can be done by setting the dependency
package name as a key on the `TransformResult.IncludeDependencyPackages` map. If the package is already a dependency of
this package, it will have no effect.

When Go compiles a package, it first collects and compiles its dependencies. Go then expects all dependencies are
compiled before the current package is compiled. Therefore, any dependencies added to this map must have already been
compiled. And it must also be resolvable. `go list -f "{{.Export}}" -export qualified/pkg/path` is used to obtain the
package file.

Users are encouraged to have their transformer code _and_ their runtime code explicitly reference the package that may
be needed _somewhere_ in code so that it is included as a `go.mod` dependency at compile time and runtime. In cases
where the transformer is compiled somewhere differently than the code that uses it is compiled, this can still result in
cases where the dependency is not yet compiled. In these cases, it is encouraged to build the transformer where the code
is built, or if that can't be done, technically `go build` can be done on the package as needed.

#### Caching

Go uses a concept of a "build ID" for caching output and determining whether to re-run. This is built on a set of
slash-delimited hashes: a leading hash representing input called an "action ID", a trailing hash representing output
called a "content ID" (which may be unset if not yet compiled), and any content in between. See comments at the top of
[buildid.go](https://github.com/golang/go/blob/go1.19.4/src/cmd/go/internal/work/buildid.go) in the Go source if curious
about details.

The build ID can be affected by content changes, Go version changes, build tag changes, different build flags, etc.
Superpose leverages this behavior by just altering the existing action IDs with reproducible dimension-specific hashes
for the other dimensions and caches the results in its own cache. Since this hash is built by dimension name and not
patched content, it can be stale if the transformer changes. So a required `Version` must be set in the Superpose
config.

`Version` should be unique for each change of a transformer that would alter code. Otherwise old cached builds from a
previous version of the same transformer may be used. Many developers may choose to use
`superpose.MustLoadCurrentExeContentID` which is the content ID of the current executable (so it changes when the exe
changes). This is a reasonable default choice but it has two downsides:

* It runs `go tool buildid <current exe>` on every single Go compile/link command. So now every package that has to be
  compiled will run this fast separate process, but it's so fast it's usually negligible.
* Cache will be invalidated for the slightest change to the transformer, even if it doesn't result in code changes to
  the transformed output.

If either of these are a concern, the `Version` field can be manually maintained.

#### Additional flags

Executables for `toolexec` built with Superpose already accept flags like `-verbose` and `-buildtags`. Users can add
their own options to be set by a user using `superpose.Config.AdditionalFlags`. Don't forget to properly quote the flags
when compiling, e.g.:

    go build -toolexec "/path/to/my-transformer -myflag flag value" some_code.go

#### Development and debugging

Effort has not currently been made to support step-based debuggers in toolexec. Therefore, the only approach to having
development/debugging details is to use logging.

During development, `superpose.Config.Verbose` can be `true` to show a lot of output during compilation. It can also be
set to true via the `-verbose` flag on the `toolexec` executable. Verbose will also include any logs to
`ctx.Superpose.Debugf` on the context inside the transformer. Also, `TransformResult.LogPatchedFiles` can be set to
`true` on the transformer result to have full patched files dumped via that same logging mechanism (so still only
visible if `Verbose` is set).

## How it works in detail

### High-level Go compilation primer

When `go build` is run, here's (mostly) what happens:

* `compile -V=full` is called to get the tool build ID to affect build IDs of the compiler's inputs/outputs
* `compile` is run for each package, with dependencies run before dependents
  * All files in the package are provided as arguments
  * A build ID is provided which is just the action ID (i.e. a unique hash of the content to compile)
    * If this package appears to have been built in the past for this action ID, compile is not called for it. Use
      `go env GOCACHE` to see where by default these are cached
  * A temp output location is given for compilation results
  * An `importcfg` is given which is a file containing a list of dependency packages already compiled that the
    package being compiled needs
  * Compilation is performed
* `link` is called to build the executable
  * An `importcfg` is given which contains all built dependency packages for the entire program
  * Link builds the executable

When `-toolexec` is added to `go` build calls, instead of the above steps executing directly, that tool is called for
each of the above steps where the compile/link/etc executables with their args just become the tool's args. Therefore
Superpose just intercepts `-toolexec` calls.

### On `compile`

When `toolexec` is executed for the compile step, Superpose does two steps defined below - "compile dimensions" and
"build bridge". Then it continues the compilation, possibly using updated arguments from the last step.

#### Compile dimensions

If any transformers apply to the given package and it that package has not already been compiled in that dimension
before for its given action ID, we run the package through the transformers as described below.

* [Load the package](https://pkg.go.dev/golang.org/x/tools/go/packages#Load)
* Call transform on the package to get patches
* For all imports in the package to other applicable-to-that-dimension packages, add patches to replace those import
  paths with the mangled dimension path equivalents
* For all in-vars referencing the dimension, patch them to be set to `true`
* If `AddLineDirectives: true`, for every file that has a patch on it, add a line directive at the top of the file
  telling the compiler to treat it as the original file name
* Apply all patches as temporary files
* Copy the original compile args but replace all patched file paths with their patched file locations
* Update the package argument of compile args to dimension-mangled path
* Update the build ID argument of compile args for a derived hash for the dimension
* Update the `importcfg` argument to a temp `importcfg` file containing updated dependencies that are applicable to
  this dimension and containing dependencies that were explicitly asked to be included by the transformer
* Update the output of the config to a temp file placeholder
* Run compile
* Copy the built package file to the Superpose build cache
* Add metadata in the Superpose build cache containing explicitly-requested dependencies to include

#### Build bridge

If there are any bridge function vars in the package:

* Build a temp init file that, for each bridge function var
  * Imports the dimension referenced if not already done
  * Adds an init statement that populates that var with a reference to the bridge function from the other dimension
* Update compile args by adding a temp init file to the end of the to-be-compiled file list
* Update `importcfg` compile arg with a new file that contains the contents of the existing file and adds new package
  references for the dimension-specific packages that were imported

### On `link`

Before the downstream `link` call is performed, the following argument alterations are made:

* Create a new `importcfg` file that has the contents of the old one
* For every package in the `importcfg` file that applies to a dimension, add the dimension-specific package too
* Load dimension-package metadata and add all explicitly included dependencies to the `importcfg` file if not already
  there

## Caveats

TODO(cretz):
* reflect.Type.PkgPath() is the dimension package
  * But even that can be patched if it must be
* Perf and mem size
* Types that can't cross the boundary
* "internal" packages

## Why

At [Temporal](https://temporal.io/), workflows in Go are written using our SDK. Workflow code is required to be
deterministic and isolated. Currently, Temporal just asks that users do not use the non-deterministic constructs in Go
(i.e. async constructs, external stuff, map ranging, global state mutation). This is part of a research project to see
if we can make an insecure sandbox that does make those constructs deterministic so the code doesn't have to concern
itself with safety. So we can make map ranging deterministic, do goroutine-local globals, use more deterministic async
constructs, and somewhat restrict external system access in an acceptably-not-foolproof way.

## TODO

* CI
* Support more options for compile time alteration including:
  * Wrapping the entire `go` command and injecting `toolexec` on `build`, e.g. `my-go build ...` would become
    `go build -toolexec "/path/to/my-go toolexec"`
  * `go:generate` or manual code generation that writes entire patched set of source somewhere for easy compilation
* Support altering primary code instead of just other dimensions
  * Was out of scope for initial needs
* Update [example/maporder](example/maporder) to support insertion-based ordering
* Add an example for "globals sandbox" which replaces all globals and global access with a wrapper and does a
  goroutine-local approach to maintaining state
* Tests:
  * `internal` package transformed
  * Stack trace and debugging
* Support other build flags like `-modfile` and really anything