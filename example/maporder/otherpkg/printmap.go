package otherpkg

import (
	"fmt"
	"runtime"
)

func PrintMap(m map[string]string) {
	_, file, line, _ := runtime.Caller(0)
	fmt.Printf("Printing map at file %v, line: %v\n", file, line)
	for k, v := range m {
		fmt.Printf("Key: %v, Value: %v\n", k, v)
	}
}
