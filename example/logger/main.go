package main

import "log"

func Log(s string) {
	log.Print(s)
}

var normalLog = Log
var alteredLog func(s string) //alterlog:Log

func main() {
	normalLog("Hello, world!")
	alteredLog("Hello, world!")
}
