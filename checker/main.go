package main

import (
	checker "checker/src"
	"os"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 5 {
		os.Exit(1)
	}

	lang := strings.ToLower(strings.TrimSpace(os.Args[1]))
	version := strings.TrimSpace(os.Args[2])
	file := os.Args[3]
	mode := strings.ToLower(strings.TrimSpace(os.Args[4]))

	payload := ""
	if len(os.Args) > 5 {
		payload = os.Args[5]
	}

	timeLimitMS := int64(0)
	if len(os.Args) > 6 {
		if v, err := strconv.ParseInt(strings.TrimSpace(os.Args[6]), 10, 64); err == nil && v >= 0 {
			timeLimitMS = v
		}
	}

	memoryLimitKB := int64(0)
	if len(os.Args) > 7 {
		if v, err := strconv.ParseInt(strings.TrimSpace(os.Args[7]), 10, 64); err == nil && v >= 0 {
			memoryLimitKB = v
		}
	}

	var err error

	switch lang {
	case "c":
		err = checker.RunC(file, mode, payload, version, timeLimitMS, memoryLimitKB)
	case "cpp", "c++":
		err = checker.RunCpp(file, mode, payload, version, timeLimitMS, memoryLimitKB)
	case "go":
		err = checker.RunGo(file, mode, payload, version, timeLimitMS, memoryLimitKB)
	default:
		os.Exit(1)
	}

	if err != nil {
		os.Exit(1)
	}
}
