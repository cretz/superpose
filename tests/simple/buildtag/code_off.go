//go:build !some_build_tag

package buildtag

func ReturnString() string { return "build tag off" }
