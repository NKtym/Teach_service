package main

import "fmt"

func main() {
	fmt.Println("Start")
	x := 10
	y := 2
	fmt.Println("Result:", x/y)
	value := 1
	ptr := &value
	fmt.Println("Pointer value:", *ptr)
	fmt.Println("OK")
}
