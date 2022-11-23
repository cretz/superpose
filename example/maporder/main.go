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

func main() {
	PrintMap()
	sortedPrintMap()
	// insertionPrintMap()
}

func PrintMap() {
	// Can check which dimension we are in
	switch {
	case inSorted:
		fmt.Println("Ordered print map via sorted iteration:")
	// case inInsertion:
	// 	fmt.Println("Ordered print map by insertion order:")
	default:
		fmt.Println("Normal print map:")
	}

	// Do print
	otherpkg.PrintMap(someMap)
}

var sortedPrintMap func() //maporder_sorted:PrintMap
var inSorted bool         //maporder_sorted:<in>

// var insertionPrintMap func() //maporder_insertion:PrintMap
// var inInsertion bool         //maporder_insertion:<in>
