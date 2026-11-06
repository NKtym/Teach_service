package main

import (
	"bufio"
	"fmt"
	"os"
)

func isValid(s string) bool {
	stack := make([]rune, 0, len(s))

	pairs := map[rune]rune{
		')': '(',
		']': '[',
		'}': '{',
	}

	for _, ch := range s {
		switch ch {
		case '(', '[', '{':
			stack = append(stack, ch)
		case ')', ']', '}':
			if len(stack) == 0 {
				return false
			}
			top := stack[len(stack)-1]
			if top != pairs[ch] {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}

	return len(stack) == 0
}

func main() {
	in := bufio.NewReader(os.Stdin)

	var s string
	fmt.Fscan(in, &s)

	if isValid(s) {
		fmt.Println("YES")
	} else {
		fmt.Println("NO")
	}
}
