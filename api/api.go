package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type groupItem struct {
	UUID   string `json:"uuid"`
	Prefix string `json:"group_prefix"`
	Number int    `json:"group_number"`
	Name   string `json:"group_name"`
}

func getTaskByCourse(courseUUID, taskUUID string) (*taskMeta, error) {
	var (
		query string
		args  []any

		rowTaskUUID    string
		taskMode       string
		taskTitle      string
		taskText       string
		expectedOutput string
		allowedJSON    string
		timeLimitMS    sql.NullInt64
		memoryLimitKB  sql.NullInt64
	)

	if strings.TrimSpace(taskUUID) != "" {
		query = `
			SELECT
				task_uuid,
				task_mode,
				task_title,
				task_text,
				result,
				allowed_lang,
				time_limit_ms,
				memory_limit_kb
			FROM tasks
			WHERE course_uuid = $1
			  AND task_uuid = $2
			LIMIT 1
		`
		args = []any{courseUUID, taskUUID}
	} else {
		query = `
			SELECT
				task_uuid,
				task_mode,
				task_title,
				task_text,
				result,
				allowed_lang,
				time_limit_ms,
				memory_limit_kb
			FROM tasks
			WHERE course_uuid = $1
			ORDER BY created_at DESC
			LIMIT 1
		`
		args = []any{courseUUID}
	}

	err := db.QueryRow(query, args...).Scan(
		&rowTaskUUID,
		&taskMode,
		&taskTitle,
		&taskText,
		&expectedOutput,
		&allowedJSON,
		&timeLimitMS,
		&memoryLimitKB,
	)
	if err != nil {
		return nil, err
	}

	meta := &taskMeta{
		TaskUUID:         rowTaskUUID,
		TaskMode:         taskMode,
		TaskTitle:        taskTitle,
		TaskText:         taskText,
		ExpectedOutput:   expectedOutput,
		AllowedLanguages: allowedLanguagesFromJSON(allowedJSON),
	}

	if timeLimitMS.Valid {
		v := timeLimitMS.Int64
		meta.TimeLimitMS = &v
	}
	if memoryLimitKB.Valid {
		v := memoryLimitKB.Int64
		meta.MemoryLimitKB = &v
	}

	return meta, nil
}

func checkHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	userUUID, role, err := getRequestUserInfo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if role != "student" {
		http.Error(w, "Only students can submit solutions", http.StatusForbidden)
		return
	}
	studentUUID := strings.TrimSpace(r.FormValue("student_uuid"))
	if studentUUID == "" {
		studentUUID = userUUID
	}
	if studentUUID == "" {
		http.Error(w, "missing student uuid", http.StatusBadRequest)
		return
	}

	courseUUID := strings.TrimSpace(r.FormValue("course_uuid"))
	taskUUID := strings.TrimSpace(r.FormValue("task_uuid"))
	if courseUUID == "" {
		http.Error(w, "missing course_uuid", http.StatusBadRequest)
		return
	}

	task, err := getTaskByCourse(courseUUID, taskUUID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load task", http.StatusInternalServerError)
		return
	}

	code := strings.TrimSpace(r.FormValue("code"))
	if code == "" {
		http.Error(w, "Empty code", http.StatusBadRequest)
		return
	}

	langRaw := strings.TrimSpace(r.FormValue("language"))
	if langRaw == "" {
		langRaw = "cpp"
	}

	lang, ext, err := normalizeLanguage(langRaw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !languageAllowed(lang, task.AllowedLanguages) {
		http.Error(w, "Language is not allowed for this task", http.StatusForbidden)
		return
	}

	versionRaw := strings.TrimSpace(r.FormValue("version"))
	if versionRaw == "" {
		switch lang {
		case "c":
			versionRaw = "c17"
		case "cpp":
			versionRaw = "c++17"
		case "go":
			versionRaw = "go1.26"
		}
	}

	version, err := normalizeVersion(lang, versionRaw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	taskHash, err := taskFingerprint(task, courseUUID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	codeHash := hashCode(code)

	cachedHash, cachedTaskHash, cachedOutput, found, err := getCachedSubmission(task.TaskUUID, studentUUID, lang, version)
	if err != nil {
		http.Error(w, "Cannot load previous submission", http.StatusInternalServerError)
		return
	}

	if found && cachedHash == codeHash && cachedTaskHash == taskHash && strings.TrimSpace(cachedOutput) != "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if role == "student" {
			_, _ = w.Write(filterCheckerOutputForStudent([]byte(cachedOutput)))
		} else {
			_, _ = w.Write([]byte(cachedOutput))
		}
		return
	}

	filePath, err := saveTextSubmissionFile(courseUUID, task.TaskUUID, studentUUID, lang, version, code, ext)
	if err != nil {
		http.Error(w, "Cannot write file", http.StatusInternalServerError)
		return
	}

	mode := strings.ToLower(strings.TrimSpace(task.TaskMode))
	if mode == "" {
		mode = "expected"
	}

	payload := ""
	switch mode {
	case "tests":
		payload = taskTestsDir(courseUUID, task.TaskUUID)
	case "expected":
		payload = canonicalizeText(task.ExpectedOutput)
	case "none":
		payload = ""
	default:
		http.Error(w, "Unsupported task mode", http.StatusBadRequest)
		return
	}

	submissionUUID := uuid.NewString()
	out, runErr := runChecker(lang, version, filePath, mode, payload, task.TimeLimitMS, task.MemoryLimitKB)
	if runErr != nil && len(strings.TrimSpace(string(out))) == 0 {
		http.Error(w, "Checker failed", http.StatusInternalServerError)
		return
	}

	if err := saveSubmissionToDB(submissionUUID, courseUUID, task.TaskUUID, studentUUID, lang, version, filePath, codeHash, taskHash, string(out)); err != nil {
		http.Error(w, "Cannot save submission metadata", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if role == "student" {
		_, _ = w.Write(filterCheckerOutputForStudent(out))
		return
	}
	_, _ = w.Write(out)
}

func checkFileHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Invalid multipart form", http.StatusBadRequest)
		return
	}

	userUUID, userRole, err := getRequestUserInfo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if userRole != "student" {
		http.Error(w, "Only students can submit solutions", http.StatusForbidden)
		return
	}
	studentUUID := strings.TrimSpace(r.FormValue("student_uuid"))
	if studentUUID == "" {
		studentUUID = userUUID
	}
	if studentUUID == "" {
		http.Error(w, "missing student uuid", http.StatusBadRequest)
		return
	}

	courseUUID := strings.TrimSpace(r.FormValue("course_uuid"))
	taskUUID := strings.TrimSpace(r.FormValue("task_uuid"))
	if courseUUID == "" {
		http.Error(w, "missing course_uuid", http.StatusBadRequest)
		return
	}

	task, err := getTaskByCourse(courseUUID, taskUUID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load task", http.StatusInternalServerError)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File upload error", http.StatusBadRequest)
		return
	}
	defer file.Close()

	langRaw := strings.TrimSpace(r.FormValue("language"))
	if langRaw == "" {
		switch strings.ToLower(filepath.Ext(header.Filename)) {
		case ".go":
			langRaw = "go"
		case ".c":
			langRaw = "c"
		case ".cpp", ".cc", ".cxx":
			langRaw = "cpp"
		default:
			http.Error(w, "Cannot detect language, choose it manually", http.StatusBadRequest)
			return
		}
	}

	lang, ext, err := normalizeLanguage(langRaw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !languageAllowed(lang, task.AllowedLanguages) {
		http.Error(w, "Language is not allowed for this task", http.StatusForbidden)
		return
	}

	versionRaw := strings.TrimSpace(r.FormValue("version"))
	if versionRaw == "" {
		switch lang {
		case "c":
			versionRaw = "c17"
		case "cpp":
			versionRaw = "c++17"
		case "go":
			versionRaw = "go1.26"
		}
	}

	version, err := normalizeVersion(lang, versionRaw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	dir := submissionDir(courseUUID, task.TaskUUID, studentUUID, lang, version)
	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, "Cannot create submission dir", http.StatusInternalServerError)
		return
	}

	tempFile, err := os.CreateTemp(dir, "upload-*.tmp")
	if err != nil {
		http.Error(w, "Cannot create temp file", http.StatusInternalServerError)
		return
	}

	codeHash, copyErr := hashReaderToTemp(file, tempFile)
	closeErr := tempFile.Close()
	if copyErr != nil {
		_ = os.Remove(tempFile.Name())
		http.Error(w, "Cannot save uploaded file", http.StatusInternalServerError)
		return
	}
	if closeErr != nil {
		_ = os.Remove(tempFile.Name())
		http.Error(w, "Cannot finish uploaded file", http.StatusInternalServerError)
		return
	}

	taskHash, err := taskFingerprint(task, courseUUID)
	if err != nil {
		_ = os.Remove(tempFile.Name())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cachedHash, cachedTaskHash, cachedOutput, found, err := getCachedSubmission(task.TaskUUID, studentUUID, lang, version)
	if err != nil {
		_ = os.Remove(tempFile.Name())
		http.Error(w, "Cannot load previous submission", http.StatusInternalServerError)
		return
	}

	finalPath := submissionFilePath(courseUUID, task.TaskUUID, studentUUID, lang, version, ext)

	if found && cachedHash == codeHash && cachedTaskHash == taskHash && strings.TrimSpace(cachedOutput) != "" {
		_ = os.Remove(tempFile.Name())
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if userRole == "student" {
			_, _ = w.Write(filterCheckerOutputForStudent([]byte(cachedOutput)))
		} else {
			_, _ = w.Write([]byte(cachedOutput))
		}
		return
	}

	if err := replaceTempWithFinal(tempFile.Name(), finalPath); err != nil {
		http.Error(w, "Cannot save uploaded file", http.StatusInternalServerError)
		return
	}

	mode := strings.ToLower(strings.TrimSpace(task.TaskMode))
	if mode == "" {
		mode = "expected"
	}

	payload := ""
	switch mode {
	case "tests":
		payload = taskTestsDir(courseUUID, task.TaskUUID)
	case "expected":
		payload = canonicalizeText(task.ExpectedOutput)
	case "none":
		payload = ""
	default:
		http.Error(w, "Unsupported task mode", http.StatusBadRequest)
		return
	}

	submissionUUID := uuid.NewString()
	out, runErr := runChecker(lang, version, finalPath, mode, payload, task.TimeLimitMS, task.MemoryLimitKB)
	if runErr != nil && len(strings.TrimSpace(string(out))) == 0 {
		http.Error(w, "Checker failed", http.StatusInternalServerError)
		return
	}

	if err := saveSubmissionToDB(submissionUUID, courseUUID, task.TaskUUID, studentUUID, lang, version, finalPath, codeHash, taskHash, string(out)); err != nil {
		http.Error(w, "Cannot save submission metadata", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if userRole == "student" {
		_, _ = w.Write(filterCheckerOutputForStudent(out))
		return
	}
	_, _ = w.Write(out)
}

func listGroupsHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET", http.StatusMethodNotAllowed)
		return
	}

	rows, err := db.Query(`
		SELECT
			group_uuid,
			COALESCE(group_prefix, regexp_replace(group_name, '\\d+$', '')) AS group_prefix,
			COALESCE(group_number, NULLIF(regexp_replace(group_name, '^\\D+', ''), '')::int) AS group_number,
			group_name
		FROM student_groups
		ORDER BY
			COALESCE(group_prefix, regexp_replace(group_name, '\\d+$', '')),
			COALESCE(group_number, NULLIF(regexp_replace(group_name, '^\\D+', ''), '')::int),
			group_name
	`)
	if err != nil {
		http.Error(w, "Cannot load groups", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var groups []groupItem
	for rows.Next() {
		var g groupItem
		var number sql.NullInt64
		if err := rows.Scan(&g.UUID, &g.Prefix, &number, &g.Name); err != nil {
			http.Error(w, "Cannot read groups", http.StatusInternalServerError)
			return
		}
		if number.Valid {
			g.Number = int(number.Int64)
		}
		groups = append(groups, g)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(groups)
}

func main() {
	if err := initDB(); err != nil {
		panic(err)
	}
	if err := ensureSubmissionAttemptsSchema(); err != nil {
		panic(err)
	}
	defer closeDB()

	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/groups", listGroupsHandler)

	http.HandleFunc("/teacher/course", createCourseHandler)
	http.HandleFunc("/teacher/course/update", teacherUpdateCourseHandler)
	http.HandleFunc("/teacher/course/delete", teacherDeleteCourseHandler)
	http.HandleFunc("/teacher/task/update", teacherUpdateTaskHandler)
	http.HandleFunc("/teacher/task/delete", teacherDeleteTaskHandler)
	http.HandleFunc("/teacher/task/edit-data", teacherTaskEditDataHandler)
	http.HandleFunc("/teacher/courses", teacherCoursesHandler)
	http.HandleFunc("/teacher/course-list", teacherCourseListHandler)
	http.HandleFunc("/teacher/course/results", courseResultsHandler)
	http.HandleFunc("/teacher/course/history", courseSubmissionHistoryHandler)

	http.HandleFunc("/student/courses", listStudentCoursesHandler)
	http.HandleFunc("/student/course", studentCourseHandler)

	http.HandleFunc("/check", checkHandler)
	http.HandleFunc("/check-file", checkFileHandler)

	fmt.Println("API running on :8081")
	_ = http.ListenAndServe(":8081", nil)
}
