package checker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type CheckerTestResult struct {
	Index    int    `json:"index"`
	Visible  bool   `json:"visible"`
	Passed   bool   `json:"passed"`
	Input    string `json:"input,omitempty"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Error    string `json:"error,omitempty"`
}

type RunResult struct {
	Mode          string              `json:"mode"`
	Compiled      bool                `json:"compiled"`
	Passed        int                 `json:"passed"`
	Total         int                 `json:"total"`
	VisiblePassed int                 `json:"visible_passed,omitempty"`
	VisibleTotal  int                 `json:"visible_total,omitempty"`
	HiddenPassed  int                 `json:"hidden_passed,omitempty"`
	HiddenTotal   int                 `json:"hidden_total,omitempty"`
	GlobalErrors  []string            `json:"global_errors,omitempty"`
	Tests         []CheckerTestResult `json:"tests,omitempty"`
	Summary       string              `json:"summary"`
}

func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

func emitResult(result RunResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fallback := `{"mode":"unknown","compiled":false,"passed":0,"total":0,"global_errors":["cannot marshal result"]}`
		fmt.Println(fallback)
		return fmt.Errorf("cannot marshal result: %w", err)
	}

	fmt.Println(string(data))

	if len(result.GlobalErrors) > 0 || result.Passed < result.Total {
		return fmt.Errorf("Найдены ошибки")
	}

	return nil
}

func filterCppcheckOutput(out string) string {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	lines := strings.Split(strings.TrimSpace(out), "\n")

	var kept []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		low := strings.ToLower(line)

		if strings.Contains(low, "information:") {
			continue
		}
		if strings.Contains(low, "missingincludesystem") {
			continue
		}
		if strings.Contains(low, "checkersreport") {
			continue
		}
		if strings.HasPrefix(low, "checking ") {
			continue
		}

		if strings.Contains(low, ": error:") ||
			strings.Contains(low, ": warning:") ||
			strings.Contains(low, ": style:") {
			kept = append(kept, line)
		}
	}

	return strings.Join(kept, "\n")
}

type testCase struct {
	Name       string
	InputPath  string
	OutputPath string
	Visible    bool
}

type taskTestManifestItem struct {
	Index   int  `json:"index"`
	Visible bool `json:"visible"`
}

type taskTestManifest struct {
	Tests []taskTestManifestItem `json:"tests"`
}

func loadVisibilityManifest(testsDir string) (map[int]bool, error) {
	manifestPath := filepath.Join(testsDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[int]bool{}, nil
		}
		return nil, err
	}

	var manifest taskTestManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}

	result := make(map[int]bool, len(manifest.Tests))
	for _, item := range manifest.Tests {
		result[item.Index] = item.Visible
	}
	return result, nil
}

func collectTestCases(testsDir string) ([]testCase, error) {
	testsDir = strings.TrimSpace(testsDir)
	if testsDir == "" {
		return nil, fmt.Errorf("tests directory is empty")
	}

	entries, err := os.ReadDir(testsDir)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.EqualFold(name, "manifest.json") {
			continue
		}
		if strings.HasSuffix(strings.ToLower(name), ".in") {
			names = append(names, name)
		}
	}

	sort.Strings(names)

	if len(names) == 0 {
		return nil, fmt.Errorf("no input tests found in %s", testsDir)
	}

	visMap, err := loadVisibilityManifest(testsDir)
	if err != nil {
		return nil, err
	}

	tests := make([]testCase, 0, len(names))
	for i, inName := range names {
		base := strings.TrimSuffix(inName, filepath.Ext(inName))
		outName := base + ".out"

		inputPath := filepath.Join(testsDir, inName)
		outputPath := filepath.Join(testsDir, outName)

		if _, err := os.Stat(outputPath); err != nil {
			return nil, fmt.Errorf("missing output file for test %s", inName)
		}

		tests = append(tests, testCase{
			Name:       base,
			InputPath:  inputPath,
			OutputPath: outputPath,
			Visible:    visMap[i+1],
		})
	}

	return tests, nil
}

func memoryWrapperCommand(programPath string, memoryLimitKB int64) string {
	if memoryLimitKB <= 0 {
		return fmt.Sprintf("exec %s", programPath)
	}
	return fmt.Sprintf("ulimit -v %d; exec %s", memoryLimitKB, programPath)
}

func classifyRunError(runErr error, stderr string, memoryLimitKB int64) string {
	msg := strings.ToLower(strings.TrimSpace(stderr))
	errText := strings.ToLower(runErr.Error())

	if memoryLimitKB > 0 && (strings.Contains(msg, "cannot allocate memory") ||
		strings.Contains(msg, "out of memory") ||
		strings.Contains(msg, "killed") ||
		strings.Contains(errText, "signal: killed") ||
		strings.Contains(errText, "signal: segmentation fault") ||
		strings.Contains(errText, "segmentation fault") ||
		strings.Contains(errText, "exit status 137") ||
		strings.Contains(errText, "exit status 139")) {
		return "превышен лимит памяти"
	}

	if strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "time limit") {
		return "превышен лимит времени"
	}

	return runErr.Error()
}

func runProgramNoInput(programPath string, timeout time.Duration, memoryLimitKB int64) (string, error) {
	cmd := exec.Command("sh", "-c", memoryWrapperCommand(programPath, memoryLimitKB))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case runErr := <-done:
		output := normalizeText(stdout.String())
		if runErr != nil {
			return output, fmt.Errorf(classifyRunError(runErr, stderr.String(), memoryLimitKB))
		}
		return output, nil
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return normalizeText(stdout.String()), fmt.Errorf("превышен лимит времени")
	}
}

func runProgramWithInput(programPath, inputPath string, timeout time.Duration, memoryLimitKB int64) (string, error) {
	inFile, err := os.Open(inputPath)
	if err != nil {
		return "", err
	}
	defer inFile.Close()

	cmd := exec.Command("sh", "-c", memoryWrapperCommand(programPath, memoryLimitKB))
	cmd.Stdin = inFile

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case runErr := <-done:
		output := normalizeText(stdout.String())
		if runErr != nil {
			return output, fmt.Errorf(classifyRunError(runErr, stderr.String(), memoryLimitKB))
		}
		return output, nil
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return normalizeText(stdout.String()), fmt.Errorf("превышен лимит времени")
	}
}

func runTests(programPath, testsDir string, timeLimitMS, memoryLimitKB int64) ([]CheckerTestResult, error) {
	tests, err := collectTestCases(testsDir)
	if err != nil {
		return nil, err
	}

	timeout := timeLimitMSToDuration(timeLimitMS)

	results := make([]CheckerTestResult, 0, len(tests))

	for i, test := range tests {
		inputBytes, err := os.ReadFile(test.InputPath)
		if err != nil {
			results = append(results, CheckerTestResult{
				Index:   i + 1,
				Visible: test.Visible,
				Passed:  false,
				Error:   "не удалось прочитать входной файл: " + err.Error(),
			})
			continue
		}

		expectedBytes, err := os.ReadFile(test.OutputPath)
		if err != nil {
			results = append(results, CheckerTestResult{
				Index:   i + 1,
				Visible: test.Visible,
				Passed:  false,
				Error:   "не удалось прочитать ожидаемый вывод: " + err.Error(),
			})
			continue
		}

		actual, runErr := runProgramWithInput(programPath, test.InputPath, timeout, memoryLimitKB)
		expected := normalizeText(string(expectedBytes))

		item := CheckerTestResult{
			Index:    i + 1,
			Visible:  test.Visible,
			Input:    string(inputBytes),
			Expected: expected,
			Actual:   actual,
			Passed:   runErr == nil && actual == expected,
		}

		if runErr != nil {
			item.Error = runErr.Error()
		}
		if item.Passed {
			item.Error = ""
		}

		results = append(results, item)
	}

	return results, nil
}

func timeLimitMSToDuration(ms int64) time.Duration {
	if ms <= 0 {
		return 2 * time.Second
	}
	return time.Duration(ms) * time.Millisecond
}

func run(file, mode, payload, langName, compiler, std string, timeLimitMS, memoryLimitKB int64) error {
	result := RunResult{
		Mode:         strings.ToLower(strings.TrimSpace(mode)),
		Tests:        make([]CheckerTestResult, 0),
		GlobalErrors: make([]string, 0),
	}

	if result.Mode == "" {
		result.Mode = "expected"
	}

	cppcheck := exec.Command(
		"cppcheck",
		"--enable=all",
		"--quiet",
		"--std="+std,
		"--template={file}:{line}:{column}: {severity}: {message} [{id}]",
		"--suppress=missingIncludeSystem",
		"--suppress=checkersReport",
		file,
	)

	cppOut, _ := cppcheck.CombinedOutput()
	filtered := filterCppcheckOutput(string(cppOut))
	if strings.TrimSpace(filtered) != "" {
		result.GlobalErrors = append(result.GlobalErrors, "Статический анализатор:\n"+filtered)
	}

	compile := exec.Command(compiler, "-std="+std, file, "-o", "program")
	buildOut, err := compile.CombinedOutput()
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
		if len(result.GlobalErrors) == 0 {
			result.Summary = "Проверки корректности кода пройдены успешно"
		} else {
			result.Summary = "Проверка завершена с ошибкой"
		}
	default:
		result.Summary = "Проверка завершена"
	}

	return emitResult(result)
}

func RunC(file string, mode string, payload string, version string, timeLimitMS int64, memoryLimitKB int64) error {
	switch strings.ToLower(strings.TrimSpace(version)) {
	case "c11", "c17", "c23":
		return run(file, mode, payload, "C", "gcc", strings.ToLower(version), timeLimitMS, memoryLimitKB)
	default:
		result := RunResult{
			Mode:         strings.ToLower(strings.TrimSpace(mode)),
			GlobalErrors: []string{fmt.Sprintf("unsupported C version: %s", version)},
			Summary:      "Проверка завершена с ошибкой",
		}
		return emitResult(result)
	}
}

func RunCpp(file string, mode string, payload string, version string, timeLimitMS int64, memoryLimitKB int64) error {
	switch strings.ToLower(strings.TrimSpace(version)) {
	case "c++11", "c++14", "c++17", "c++20", "c++23":
		return run(file, mode, payload, "C++", "g++", strings.ToLower(version), timeLimitMS, memoryLimitKB)
	default:
		result := RunResult{
			Mode:         strings.ToLower(strings.TrimSpace(mode)),
			GlobalErrors: []string{fmt.Sprintf("unsupported C++ version: %s", version)},
			Summary:      "Проверка завершена с ошибкой",
		}
		return emitResult(result)
	}
}
