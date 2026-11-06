package checker

import (
	"fmt"
	"os/exec"
	"strings"
)

func goLangFlag(version string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(version)) {
	case "go1.18", "go1.21", "go1.25", "go1.26":
		return "-lang=" + strings.ToLower(strings.TrimSpace(version)), nil
	default:
		return "", fmt.Errorf("unsupported Go version: %s", version)
	}
}

func RunGo(file string, mode string, payload string, version string, timeLimitMS int64, memoryLimitKB int64) error {
	result := RunResult{
		Mode:         strings.ToLower(strings.TrimSpace(mode)),
		Tests:        make([]CheckerTestResult, 0),
		GlobalErrors: make([]string, 0),
	}

	if result.Mode == "" {
		result.Mode = "expected"
	}

	langFlag, err := goLangFlag(version)
	if err != nil {
		result.GlobalErrors = append(result.GlobalErrors, err.Error())
		result.Summary = "Проверка завершена с ошибкой"
		return emitResult(result)
	}

	vetCmd := exec.Command("go", "vet", file)
	vetOut, _ := vetCmd.CombinedOutput()
	if len(vetOut) > 0 {
		result.GlobalErrors = append(result.GlobalErrors, "Результат vet:\n"+string(vetOut))
	}

	staticCmd := exec.Command("staticcheck", file)
	staticOut, staticErr := staticCmd.CombinedOutput()
	if staticErr != nil && len(staticOut) > 0 {
		result.GlobalErrors = append(result.GlobalErrors, "Результат staticcheck:\n"+string(staticOut))
	} else if staticErr != nil && len(staticOut) == 0 {
		result.GlobalErrors = append(result.GlobalErrors, "Результат staticcheck:\n"+staticErr.Error())
	}

	compileCmd := exec.Command("go", "build", "-gcflags=command-line-arguments="+langFlag, "-o", "program", file)
	buildOut, err := compileCmd.CombinedOutput()
	result.Compiled = err == nil
	if !result.Compiled {
		result.GlobalErrors = append(result.GlobalErrors, "Ошибка компиляции:\n"+string(buildOut))
	}

	if result.Compiled {
		switch result.Mode {
		case "tests":
			tests, testErr := runTests("./program", payload, timeLimitMS, memoryLimitKB)
			if testErr != nil {
				result.GlobalErrors = append(result.GlobalErrors, "Ошибка запуска тестов:\n"+testErr.Error())
			}
			result.Tests = tests
			for _, t := range tests {
				result.Total++
				if t.Passed {
					result.Passed++
				}
				if t.Visible {
					result.VisibleTotal++
					if t.Passed {
						result.VisiblePassed++
					}
				} else {
					result.HiddenTotal++
					if t.Passed {
						result.HiddenPassed++
					}
				}
			}
		case "expected":
			output, runErr := runProgramNoInput("./program", timeLimitMSToDuration(timeLimitMS), memoryLimitKB)
			if runErr != nil {
				result.GlobalErrors = append(result.GlobalErrors, "Ошибка выполнения кода: "+runErr.Error())
			}

			expected := normalizeText(payload)
			actual := normalizeText(output)
			passed := actual == expected

			result.Total = 1
			if passed {
				result.Passed = 1
			}

			result.VisibleTotal = 1
			if passed {
				result.VisiblePassed = 1
			}

			result.Tests = []CheckerTestResult{
				{
					Index:    1,
					Visible:  true,
					Passed:   passed,
					Expected: expected,
					Actual:   actual,
				},
			}

			if !passed {
				result.GlobalErrors = append(result.GlobalErrors, fmt.Sprintf(
					"Ошибка в результате выполнения кода:\nОжидаемый результат:\n%s\nРеальный результат выполнения:\n%s",
					expected,
					actual,
				))
			}
		case "none":
			_, runErr := runProgramNoInput("./program", timeLimitMSToDuration(timeLimitMS), memoryLimitKB)
			if runErr != nil {
				result.GlobalErrors = append(result.GlobalErrors, "Ошибка выполнения кода: "+runErr.Error())
			}
		default:
			result.GlobalErrors = append(result.GlobalErrors, "unsupported task mode: "+result.Mode)
		}
	}

	switch result.Mode {
	case "tests":
		result.Summary = fmt.Sprintf("Пройдено тестов: %d/%d", result.Passed, result.Total)
	case "expected":
		result.Summary = fmt.Sprintf("Пройдено тестов: %d/%d", result.Passed, result.Total)
	case "none":
		result.Summary = "Проверки корректности кода пройдены успешно"
	default:
		result.Summary = "Проверка завершена"
	}

	return emitResult(result)
}
