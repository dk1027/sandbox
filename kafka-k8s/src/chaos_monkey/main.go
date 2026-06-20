package main

import (
	"fmt"
	"time"
)

func main() {
	fmt.Println("Chaos Monkey placeholder application started...")
	
	// While-true sleep loop to keep the pod running
	for {
		time.Sleep(10 * time.Second)
	}
}
