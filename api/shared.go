package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	storageRoot = "/storage/submissions"
	testsRoot   = "/storage/tests"
)

type taskMeta struct {
	TaskUUID         string
	TaskMode         string
	TaskTitle        string
	TaskText         string
	ExpectedOutput   string
	AllowedLanguages []string
	TimeLimitMS      *int64
	MemoryLimitKB    *int64
}

type courseTaskItem struct {
	TaskUUID         string    `json:"task_uuid"`
	TaskMode         string    `json:"task_mode"`
	TaskTitle        string    `json:"task_title"`
	TaskText         string    `json:"task_text"`
	AllowedLanguages []string  `json:"allowed_languages,omitempty"`
	TestsCount       int       `json:"tests_count"`
	TimeLimitMS      *int64    `json:"time_limit_ms,omitempty"`
	MemoryLimitKB    *int64    `json:"memory_limit_kb,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type courseOverviewItem struct {
	CourseUUID string           `json:"course_uuid"`
	CourseName string           `json:"course_name"`
	GroupUUID  string           `json:"group_uuid"`
	GroupName  string           `json:"group_name"`
	GroupUUIDs []string         `json:"group_uuids,omitempty"`
	GroupNames []string         `json:"group_names,omitempty"`
	TasksCount int              `json:"tasks_count"`
	Tasks      []courseTaskItem `json:"tasks"`
}

type teacherSubmissionItem struct {
	SubmissionUUID   string     `json:"submission_uuid"`
	StudentUUID      string     `json:"student_uuid"`
	StudentName      string     `json:"student_name"`
	StudentLogin     string     `json:"student_login"`
	StudentGroupUUID string     `json:"student_group_uuid"`
	StudentGroupName string     `json:"student_group_name"`
	Language         string     `json:"lang"`
	LanguageVersion  string     `json:"lang_version"`
	SourcePath       string     `json:"source_path"`
	SourceHash       string     `json:"source_hash"`
	CheckerOutput    string     `json:"checker_output"`
	AttemptsCount    int        `json:"attempts_count"`
	LastAttemptAt    *time.Time `json:"last_attempt_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type teacherTaskResultsResponse struct {
	CourseUUID       string                  `json:"course_uuid"`
	CourseName       string                  `json:"course_name"`
	GroupName        string                  `json:"group_name"`
	CourseGroups     []groupItem             `json:"course_groups"`
	TaskUUID         string                  `json:"task_uuid"`
	TaskMode         string                  `json:"task_mode"`
	TaskTitle        string                  `json:"task_title"`
	TaskText         string                  `json:"task_text"`
	ExpectedOutput   string                  `json:"expected_output,omitempty"`
	AllowedLanguages []string                `json:"allowed_languages"`
	TestsCount       int                     `json:"tests_count"`
	TimeLimitMS      *int64                  `json:"time_limit_ms,omitempty"`
	MemoryLimitKB    *int64                  `json:"memory_limit_kb,omitempty"`
	Submissions      []teacherSubmissionItem `json:"submissions"`
}

type teacherSubmissionHistoryItem struct {
	AttemptUUID     string    `json:"attempt_uuid"`
	AttemptNumber   int       `json:"attempt_number"`
	SubmissionUUID  string    `json:"submission_uuid"`
	Language        string    `json:"lang"`
	LanguageVersion string    `json:"lang_version"`
	SourcePath      string    `json:"source_path"`
	SourceHash      string    `json:"source_hash"`
	CheckerOutput   string    `json:"checker_output"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type teacherTaskHistoryResponse struct {
	CourseUUID       string                         `json:"course_uuid"`
	CourseName       string                         `json:"course_name"`
	GroupName        string                         `json:"group_name"`
	TaskUUID         string                         `json:"task_uuid"`
	TaskMode         string                         `json:"task_mode"`
	TaskTitle        string                         `json:"task_title"`
	TaskText         string                         `json:"task_text"`
	ExpectedOutput   string                         `json:"expected_output,omitempty"`
	AllowedLanguages []string                       `json:"allowed_languages"`
	TestsCount       int                            `json:"tests_count"`
	TimeLimitMS      *int64                         `json:"time_limit_ms,omitempty"`
	MemoryLimitKB    *int64                         `json:"memory_limit_kb,omitempty"`
	StudentUUID      string                         `json:"student_uuid"`
	StudentName      string                         `json:"student_name"`
	StudentLogin     string                         `json:"student_login"`
	AttemptsCount    int                            `json:"attempts_count"`
	Attempts         []teacherSubmissionHistoryItem `json:"attempts"`
}

type checkerTestResult struct {
	Index    int    `json:"index"`
	Visible  bool   `json:"visible"`
	Passed   bool   `json:"passed"`
	Input    string `json:"input,omitempty"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Error    string `json:"error,omitempty"`
}

type checkerRunResult struct {
	Mode          string              `json:"mode"`
	Compiled      bool                `json:"compiled"`
	Passed        int                 `json:"passed"`
	Total         int                 `json:"total"`
	VisiblePassed int                 `json:"visible_passed,omitempty"`
	VisibleTotal  int                 `json:"visible_total,omitempty"`
	HiddenPassed  int                 `json:"hidden_passed,omitempty"`
	HiddenTotal   int                 `json:"hidden_total,omitempty"`
	GlobalErrors  []string            `json:"global_errors,omitempty"`
	Tests         []checkerTestResult `json:"tests,omitempty"`
	Summary       string              `json:"summary"`
}

func enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func getRequestUserInfo(r *http.Request) (string, string, error) {
	userUUID := strings.TrimSpace(r.FormValue("user_uuid"))
	userRole := strings.TrimSpace(r.FormValue("user_role"))

	if userUUID == "" || userRole == "" {
		return "", "", fmt.Errorf("missing user info")
	}

	return userUUID, userRole, nil
}

func normalizeLanguage(lang string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "c":
		return "c", ".c", nil
	case "cpp", "c++":
		return "cpp", ".cpp", nil
	case "go":
		return "go", ".go", nil
	default:
		return "", "", fmt.Errorf("unsupported language: %s", lang)
	}
}

func normalizeVersion(lang, version string) (string, error) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	version = strings.ToLower(strings.TrimSpace(version))

	switch lang {
	case "c":
		switch version {
		case "c11", "c17", "c23":
			return version, nil
		}
		return "", fmt.Errorf("unsupported C version: %s", version)
	case "cpp":
		switch version {
		case "c++11", "c++14", "c++17", "c++20", "c++23":
			return version, nil
		}
		return "", fmt.Errorf("unsupported C++ version: %s", version)
	case "go":
		switch version {
		case "go1.18", "go1.21", "go1.25", "go1.26":
			return version, nil
		}
		return "", fmt.Errorf("unsupported Go version: %s", version)
	default:
		return "", fmt.Errorf("unsupported language: %s", lang)
	}
}

func canonicalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

func splitNonEmptyCSV(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{}
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}

	return result
}

func joinStrings(values []string, sep string) string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			filtered = append(filtered, value)
		}
	}
	return strings.Join(filtered, sep)
}

func runChecker(lang, version, filePath, mode, payload string, timeLimitMS, memoryLimitKB *int64) ([]byte, error) {
	args := []string{"exec", "checker", "/app/checker", lang, version, filePath, mode}
	args = append(args, payload)

	timeArg := "0"
	memArg := "0"
	if timeLimitMS != nil && *timeLimitMS > 0 {
		timeArg = fmt.Sprintf("%d", *timeLimitMS)
	}
	if memoryLimitKB != nil && *memoryLimitKB > 0 {
		memArg = fmt.Sprintf("%d", *memoryLimitKB)
	}
	args = append(args, timeArg, memArg)

	cmd := exec.Command("docker", args...)
	return cmd.CombinedOutput()
}

func submissionDir(courseUUID, taskUUID, userUUID, language, version string) string {
	return filepath.Join(storageRoot, courseUUID, taskUUID, userUUID, language, version)
}

func submissionFilePath(courseUUID, taskUUID, userUUID, language, version, ext string) string {
	return filepath.Join(submissionDir(courseUUID, taskUUID, userUUID, language, version), "submission"+ext)
}

func taskTestsDir(courseUUID, taskUUID string) string {
	return filepath.Join(testsRoot, courseUUID, taskUUID)
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func hashReaderToTemp(src io.Reader, tempFile *os.File) (string, error) {
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tempFile, hasher), src); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func hashTestsDir(testsDir string) (string, error) {
	entries, err := os.ReadDir(testsDir)
	if err != nil {
		return "", err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	hasher := sha256.New()
	for _, name := range names {
		path := filepath.Join(testsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		_, _ = hasher.Write([]byte(name))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(data)
		_, _ = hasher.Write([]byte{0})
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func taskFingerprint(task *taskMeta, courseUUID string) (string, error) {
	h := sha256.New()

	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(task.TaskMode))))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(canonicalizeText(task.ExpectedOutput)))
	_, _ = h.Write([]byte{0})

	if task.TimeLimitMS != nil {
		_, _ = h.Write([]byte(fmt.Sprintf("time=%d", *task.TimeLimitMS)))
	}
	_, _ = h.Write([]byte{0})

	if task.MemoryLimitKB != nil {
		_, _ = h.Write([]byte(fmt.Sprintf("mem=%d", *task.MemoryLimitKB)))
	}
	_, _ = h.Write([]byte{0})

	switch strings.ToLower(strings.TrimSpace(task.TaskMode)) {
	case "tests":
		testsHash, err := hashTestsDir(taskTestsDir(courseUUID, task.TaskUUID))
		if err != nil {
			return "", err
		}
		_, _ = h.Write([]byte(testsHash))
	case "expected":
		_, _ = h.Write([]byte("expected"))
	case "none":
		_, _ = h.Write([]byte("none"))
	default:
		return "", fmt.Errorf("unsupported task mode: %s", task.TaskMode)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func parseOptionalInt(s string) *int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

func allowedLanguagesFromJSON(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{"c", "cpp", "go"}
	}

	var langs []string
	if err := json.Unmarshal([]byte(raw), &langs); err != nil || len(langs) == 0 {
		return []string{"c", "cpp", "go"}
	}
	return langs
}

func languageAllowed(lang string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}

	lang = strings.ToLower(strings.TrimSpace(lang))
	for _, a := range allowed {
		if strings.ToLower(strings.TrimSpace(a)) == lang {
			return true
		}
	}
	return false
}

func saveSubmissionFile(path, code string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(code), 0644)
}

func saveTextSubmissionFile(courseUUID, taskUUID, studentUUID, language, version, code, ext string) (string, error) {
	path := submissionFilePath(courseUUID, taskUUID, studentUUID, language, version, ext)
	if err := saveSubmissionFile(path, code); err != nil {
		return "", err
	}
	return path, nil
}

func replaceTempWithFinal(tempPath, finalPath string) error {
	_ = os.Remove(finalPath)
	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func getCachedSubmission(taskUUID, studentUUID, language, version string) (string, string, string, bool, error) {
	var sourceHash string
	var testsHash string
	var checkerOutput string

	err := db.QueryRow(`
		SELECT source_hash, tests_hash, checker_output
		FROM submissions
		WHERE task_uuid = $1
		  AND student_uuid = $2
		  AND lang = $3
	      AND lang_version = $4
	`, taskUUID, studentUUID, language, version).Scan(&sourceHash, &testsHash, &checkerOutput)

	if err == sql.ErrNoRows {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}

	return sourceHash, testsHash, checkerOutput, true, nil
}

func ensureSubmissionAttemptsSchema() error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS submission_attempts (
			attempt_uuid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			submission_uuid UUID NOT NULL,
			course_uuid UUID NOT NULL,
			task_uuid UUID NOT NULL,
			student_uuid UUID NOT NULL,
			lang VARCHAR(10) NOT NULL,
			lang_version VARCHAR(10) NOT NULL,
			source_path TEXT NOT NULL,
			source_hash TEXT NOT NULL,
			tests_hash TEXT NOT NULL DEFAULT '',
			checker_output TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			CONSTRAINT fk_submission_attempts_submission
				FOREIGN KEY (submission_uuid)
				REFERENCES submissions(submission_uuid)
				ON DELETE CASCADE,
			CONSTRAINT fk_submission_attempts_course
				FOREIGN KEY (course_uuid)
				REFERENCES courses(course_uuid)
				ON DELETE CASCADE,
			CONSTRAINT fk_submission_attempts_task
				FOREIGN KEY (task_uuid)
				REFERENCES tasks(task_uuid)
				ON DELETE CASCADE,
			CONSTRAINT fk_submission_attempts_student
				FOREIGN KEY (student_uuid)
				REFERENCES students(student_uuid)
				ON DELETE CASCADE
		)
	`)
	if err != nil {
		return err
	}

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_submission_attempts_course_uuid ON submission_attempts(course_uuid)`,
		`CREATE INDEX IF NOT EXISTS idx_submission_attempts_task_uuid ON submission_attempts(task_uuid)`,
		`CREATE INDEX IF NOT EXISTS idx_submission_attempts_student_uuid ON submission_attempts(student_uuid)`,
		`CREATE INDEX IF NOT EXISTS idx_submission_attempts_submission_uuid ON submission_attempts(submission_uuid)`,
	}
	for _, stmt := range indexes {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}

	var attemptsCount int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM submission_attempts`).Scan(&attemptsCount); err != nil {
		return err
	}
	if attemptsCount == 0 {
		_, err = db.Exec(`
			INSERT INTO submission_attempts (
				attempt_uuid,
				submission_uuid,
				course_uuid,
				task_uuid,
				student_uuid,
				lang,
				lang_version,
				source_path,
				source_hash,
				tests_hash,
				checker_output,
				created_at,
				updated_at
			)
			SELECT
				gen_random_uuid(),
				submission_uuid,
				course_uuid,
				task_uuid,
				student_uuid,
				lang,
				lang_version,
				source_path,
				source_hash,
				tests_hash,
				checker_output,
				created_at,
				updated_at
			FROM submissions
		`)
		if err != nil {
			return err
		}
	}

	return nil
}

func saveSubmissionToDB(submissionUUID, courseUUID, taskUUID, studentUUID, language, version, sourcePath, sourceHash, testsHash, checkerOutput string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var storedSubmissionUUID string
	err = tx.QueryRow(`
		INSERT INTO submissions (
			submission_uuid,
			course_uuid,
			task_uuid,
			student_uuid,
			lang,
			lang_version,
			source_path,
			source_hash,
			tests_hash,
			checker_output
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (task_uuid, student_uuid, lang, lang_version)
		DO UPDATE SET
			source_path = EXCLUDED.source_path,
			source_hash = EXCLUDED.source_hash,
			tests_hash = EXCLUDED.tests_hash,
			checker_output = EXCLUDED.checker_output,
			updated_at = NOW()
		RETURNING submission_uuid
	`, submissionUUID, courseUUID, taskUUID, studentUUID, language, version, sourcePath, sourceHash, testsHash, checkerOutput).Scan(&storedSubmissionUUID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO submission_attempts (
			attempt_uuid,
			submission_uuid,
			course_uuid,
			task_uuid,
			student_uuid,
			lang,
			lang_version,
			source_path,
			source_hash,
			tests_hash,
			checker_output
		)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, storedSubmissionUUID, courseUUID, taskUUID, studentUUID, language, version, sourcePath, sourceHash, testsHash, checkerOutput)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func filterCheckerOutputForStudent(raw []byte) []byte {
	var res checkerRunResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return raw
	}

	var b strings.Builder

	writeLines := func(lines ...string) {
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	writeErrorBlock := func(title string, errs []string) {
		if len(errs) == 0 {
			return
		}
		writeLines(title)
		for _, e := range errs {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			b.WriteString(e)
			b.WriteString("\n")
		}
	}

	mode := strings.ToLower(strings.TrimSpace(res.Mode))

	switch mode {
	case "tests":
		writeLines("Прохождение открытых тестов")

		hasHidden := false
		for _, t := range res.Tests {
			if !t.Visible {
				hasHidden = true
				continue
			}

			if t.Passed {
				writeLines(fmt.Sprintf("Тест %d: пройден", t.Index))
				continue
			}

			writeLines(
				fmt.Sprintf("Тест %d: не пройден", t.Index),
				"Вход:",
				t.Input,
				"Ожидалось:",
				t.Expected,
				"Получено:",
				t.Actual,
			)
			if strings.TrimSpace(t.Error) != "" {
				writeLines("Ошибка:", t.Error)
			}
			writeLines("")
		}

		writeLines(fmt.Sprintf("Пройдено тестов: %d/%d", res.Passed, res.Total))
		if hasHidden {
			writeLines("Есть скрытые тесты, результаты которых не показываются.")
		}
		writeErrorBlock("Ошибки проверки:", res.GlobalErrors)

	case "expected":
		if len(res.GlobalErrors) == 0 && res.Passed == res.Total {
			writeLines("Проверки корректности кода пройдены успешно")
			break
		}
		writeLines(fmt.Sprintf("Пройдено тестов: %d/%d", res.Passed, res.Total))
		writeErrorBlock("Ошибки проверки:", res.GlobalErrors)

	case "none":
		if len(res.GlobalErrors) == 0 {
			writeLines("Проверки корректности кода пройдены успешно")
			break
		}
		writeErrorBlock("Ошибки проверки:", res.GlobalErrors)

	default:
		return raw
	}

	out := strings.TrimSpace(b.String())
	if out == "" {
		return raw
	}
	return []byte(out + "\n")
}
