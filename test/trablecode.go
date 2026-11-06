package main

import (
	"fmt"
	"time"
)

func main() {
	fmt.Println("Start")
	x := 10
	y := 0
	fmt.Println("Divide:", x/y)
	var ptr *int
	fmt.Println("Pointer:", *ptr)
	for i := 0; i < 1000; i++ {
		go func() {
			select {}
		}()
	}
	var data [][]byte
	for i := 0; i < 1000; i++ {
		data = append(data, make([]byte, 1024*1024))
	}
	for {
		time.Sleep(time.Second)
	}
	fmt.Println("Wrong result")
}
