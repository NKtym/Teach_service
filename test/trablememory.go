package main

var sink [][]byte

func main() {
	for {
		sink = append(sink, make([]byte, 10<<20))
	}
}
