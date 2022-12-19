# Superpose

**⚠️ UNDER DEVELOPMENT**

Superpose is a library for creating Go compiler wrappers/plugins that support transforming packages in other
"dimensions" and making them callable from the original package.

TODO(cretz): More docs
TODO(cretz): Make sure to test:
* An import whose package name is not related to its path
* Relative/local imports
* Explicit import aliases
* Different build tags
* Test files/packages
* go:embed
* Build caching
* Build "-a" to invalidate cache