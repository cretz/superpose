package main

import (
	"log"
	"time"

	"github.com/cretz/superpose/example/mocktime/clock"
)

func Log(msg string) { log.Print(msg) }

var LogInMockEnv func(msg string) //mocktime:Log

func main() {
	// Let's set our mock clock to 2020-01-01
	clock.NowUnixMilli = time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local).UnixMilli()

	// Log
	Log("Non-mocked begin")
	LogInMockEnv("Mocked begin")

	// Wait 2s in real time, but 30s in mocked time
	time.Sleep(2 * time.Second)
	clock.NowUnixMilli += 30000

	// Log again
	Log("Non-mocked after 2s")
	LogInMockEnv("Mocked after 2s")
}
