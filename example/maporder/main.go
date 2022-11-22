package main

import (
	"fmt"

	"github.com/cretz/superpose/example/maporder/otherpkg"
)

var someMap = map[string]string{
	"foo-key": "foo-val",
	"bar-key": "bar-val",
	"baz-key": "baz-val",
	"qux-key": "qux-val",
}

func init() {
	fmt.Println("ORIG INIT")
}

func main() {
	fmt.Println("Normal print map:")
	PrintMap()

	fmt.Println("\nSorted print map:")
	orderedPrintMap()
}

func PrintMap() {
	otherpkg.PrintMap(someMap)
}

// mapsort:PrintMap
var orderedPrintMap func()
