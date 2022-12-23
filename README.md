# Superpose

**⚠️ UNDER DEVELOPMENT**

Superpose is a library for creating Go compiler wrappers/plugins that support transforming packages in other
"dimensions" and making them callable from the original package.

See [the map order example](example/maporder) to show how you can have deterministically ordered map ranges.

## Usage

TODO(cretz): Explain
* Creation of a "transformer"
* Referencing other-dimension function
* Knowing whether you're running in another dimension
* Usage inside toolexec
* Advanced options (additional deps, cache, line directives, additional flags, etc)
* Testing

## How it Works

TODO(cretz): Sections
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
  * But even that can be patched

## TODO

* More docs
* Test framework
* Tests, including:
  * An import whose package name is not related to its path
  * Relative/local imports
  * Explicit import aliases
  * Different build tags
  * Test files/packages
  * go:embed
  * Build caching
  * Build "-a" to invalidate cache
  * Patching `runtime` package