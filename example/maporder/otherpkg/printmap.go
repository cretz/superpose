package otherpkg

import "fmt"

func PrintMap(m map[string]string) {
	for k, v := range m {
		fmt.Printf("Key: %v, Value: %v\n", k, v)
	}
}
