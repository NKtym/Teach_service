package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func isValidPartial(s string) bool {
	if strings.ContainsAny(s, "[]{}") {
		return false
	}

	balance := 0
	for _, ch := range s {
		switch ch {
		case '(':
			balance++
		case ')':
			balance--
			if balance < 0 {
				return false
			}
		}
	}

	return balance == 0
}

func main() {
	in := bufio.NewReader(os.Stdin)

	var s string
	fmt.Fscan(in, &s)

	if isValidPartial(s) {
		fmt.Println("YES")
	} else {
		fmt.Println("NO")
	}
}
