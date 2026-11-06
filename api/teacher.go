package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type taskTestManifestItem struct {
	Index   int  `json:"index"`
	Visible bool `json:"visible"`
}

type taskTestManifest struct {
	Tests []taskTestManifestItem `json:"tests"`
}

type taskTestDetailItem struct {
	Index   int    `json:"index"`
	Visible bool   `json:"visible"`
	Input   string `json:"input"`
	Output  string `json:"output"`
}

type teacherTaskEditResponse struct {
	CourseUUID       string               `json:"course_uuid"`
	CourseName       string               `json:"course_name"`
	GroupUUID        string               `json:"group_uuid"`
	GroupName        string               `json:"group_name"`
	TaskUUID         string               `json:"task_uuid"`
	TaskMode         string               `json:"task_mode"`
	TaskTitle        string               `json:"task_title"`
	TaskText         string               `json:"task_text"`
	ExpectedOutput   string               `json:"expected_output"`
	AllowedLanguages []string             `json:"allowed_languages"`
	TestsCount       int                  `json:"tests_count"`
	TimeLimitMS      *int64               `json:"time_limit_ms,omitempty"`
	MemoryLimitKB    *int64               `json:"memory_limit_kb,omitempty"`
	Tests            []taskTestDetailItem `json:"tests,omitempty"`
}

func parseCourseGroupUUIDs(r *http.Request) []string {
	values := append([]string{}, r.MultipartForm.Value["group_uuids"]...)
	if len(values) == 0 {
		values = append(values, r.Form["group_uuids"]...)
	}
	if len(values) == 0 {
		if v := strings.TrimSpace(r.FormValue("group_uuid")); v != "" {
			values = append(values, v)
		}
	}

	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func replaceCourseGroups(tx *sql.Tx, courseUUID string, groupUUIDs []string) error {
	if len(groupUUIDs) == 0 {
		return fmt.Errorf("at least one group is required")
	}

	if _, err := tx.Exec(`DELETE FROM course_groups WHERE course_uuid = $1`, courseUUID); err != nil {
		return err
	}

	for _, groupUUID := range groupUUIDs {
		var exists string
		if err := tx.QueryRow(`SELECT group_uuid FROM student_groups WHERE group_uuid = $1`, groupUUID).Scan(&exists); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO course_groups (course_uuid, group_uuid) VALUES ($1, $2)`, courseUUID, groupUUID); err != nil {
			return err
		}
	}

	return nil
}

func parseVisibilityFlags(values []string, count int) []bool {
	flags := make([]bool, count)
	for i := 0; i < count; i++ {
		if i >= len(values) {
			flags[i] = false
			continue
		}
		v := strings.ToLower(strings.TrimSpace(values[i]))
		flags[i] = v == "1" || v == "true" || v == "on" || v == "yes"
	}
	return flags
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func indexedMultipartFieldCount(form *multipart.Form, prefix string) int {
	if form == nil {
		return 0
	}

	maxIndex := -1
	for field := range form.File {
		if !strings.HasPrefix(field, prefix) {
			continue
		}

		idx, err := strconv.Atoi(strings.TrimPrefix(field, prefix))
		if err != nil || idx < 0 {
			continue
		}

		if idx > maxIndex {
			maxIndex = idx
		}
	}

	return maxIndex + 1
}

func readMultipartFileField(form *multipart.Form, field string) ([]byte, bool, error) {
	if form == nil {
		return nil, false, nil
	}
	files := form.File[field]
	if len(files) == 0 {
		return nil, false, nil
	}
	file, err := files[0].Open()
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func saveTaskTestsData(tx *sql.Tx, courseUUID, taskUUID string, inputData, outputData [][]byte, visibleFlags []bool) error {
	testsDir := taskTestsDir(courseUUID, taskUUID)

	if err := os.RemoveAll(testsDir); err != nil {
		return err
	}
	if err := os.MkdirAll(testsDir, 0755); err != nil {
		return err
	}

	manifest := taskTestManifest{
		Tests: make([]taskTestManifestItem, 0, len(inputData)),
	}

	for i := range inputData {
		inputPath := filepath.Join(testsDir, fmt.Sprintf("%03d.in", i+1))
		outputPath := filepath.Join(testsDir, fmt.Sprintf("%03d.out", i+1))

		if err := os.WriteFile(inputPath, inputData[i], 0644); err != nil {
			return err
		}
		if err := os.WriteFile(outputPath, outputData[i], 0644); err != nil {
			return err
		}

		visible := false
		if i < len(visibleFlags) {
			visible = visibleFlags[i]
		}
		manifest.Tests = append(manifest.Tests, taskTestManifestItem{
			Index:   i + 1,
			Visible: visible,
		})

		if _, err := tx.Exec(`
			INSERT INTO task_tests (
				task_uuid,
				test_index,
				visible_to_student,
				input_path,
				output_path
			)
			VALUES ($1, $2, $3, $4, $5)
		`, taskUUID, i+1, visible, inputPath, outputPath); err != nil {
			return err
		}
	}

	manifestPath := filepath.Join(testsDir, "manifest.json")
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0644); err != nil {
		return err
	}

	return nil
}

func saveTaskTests(tx *sql.Tx, courseUUID, taskUUID string, inputFiles, outputFiles []*multipart.FileHeader, visibleFlags []bool) error {
	inputData := make([][]byte, len(inputFiles))
	outputData := make([][]byte, len(outputFiles))

	for i := range inputFiles {
		inHandle, err := inputFiles[i].Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(inHandle)
		_ = inHandle.Close()
		if err != nil {
			return err
		}
		inputData[i] = data

		outHandle, err := outputFiles[i].Open()
		if err != nil {
			return err
		}
		data, err = io.ReadAll(outHandle)
		_ = outHandle.Close()
		if err != nil {
			return err
		}
		outputData[i] = data
	}

	return saveTaskTestsData(tx, courseUUID, taskUUID, inputData, outputData, visibleFlags)
}

func loadTaskTestDetails(courseUUID, taskUUID string) ([]taskTestDetailItem, error) {
	rows, err := db.Query(`
		SELECT test_index, visible_to_student, input_path, output_path
		FROM task_tests
		WHERE task_uuid = $1
		ORDER BY test_index ASC
	`, taskUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]taskTestDetailItem, 0)
	for rows.Next() {
		var item taskTestDetailItem
		var inputPath, outputPath string
		if err := rows.Scan(&item.Index, &item.Visible, &inputPath, &outputPath); err != nil {
			return nil, err
		}

		inputBytes, err := os.ReadFile(inputPath)
		if err != nil {
			return nil, err
		}
		outputBytes, err := os.ReadFile(outputPath)
		if err != nil {
			return nil, err
		}

		item.Input = string(inputBytes)
		item.Output = string(outputBytes)
		items = append(items, item)
	}

	return items, nil
}

func loadTeacherCourses(userUUID string) ([]courseOverviewItem, error) {
	rows, err := db.Query(`
		WITH course_base AS (
			SELECT
				c.course_uuid,
				c.course_name,
				COALESCE(string_agg(g.group_name, ', ' ORDER BY g.group_name), '') AS group_names,
				COALESCE(string_agg(g.group_uuid::text, ',' ORDER BY g.group_name), '') AS group_uuids,
				COALESCE((SELECT COUNT(*) FROM tasks tt WHERE tt.course_uuid = c.course_uuid), 0) AS tasks_count,
				c.created_at
			FROM courses c
			LEFT JOIN course_groups cg ON cg.course_uuid = c.course_uuid
			LEFT JOIN student_groups g ON g.group_uuid = cg.group_uuid
			WHERE c.teacher_uuid = $1
			GROUP BY c.course_uuid, c.course_name, c.created_at
		)
		SELECT
			cb.course_uuid,
			cb.course_name,
			cb.group_names,
			cb.group_uuids,
			cb.tasks_count,
			t.task_uuid,
			t.task_mode,
			t.task_title,
			t.task_text,
			t.allowed_lang,
			COALESCE((SELECT COUNT(*) FROM task_tests x WHERE x.task_uuid = t.task_uuid), 0) AS tests_count,
			t.time_limit_ms,
			t.memory_limit_kb,
			t.created_at
		FROM course_base cb
		LEFT JOIN tasks t ON t.course_uuid = cb.course_uuid
		ORDER BY cb.created_at DESC, t.created_at DESC NULLS LAST
	`, userUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]courseOverviewItem, 0)
	indexByCourse := map[string]int{}

	for rows.Next() {
		var (
			courseUUID    string
			courseName    string
			groupNamesCSV string
			groupUUIDsCSV string
			tasksCount    int

			taskUUID      sql.NullString
			taskMode      sql.NullString
			taskTitle     sql.NullString
			taskText      sql.NullString
			allowedJSON   sql.NullString
			testsCount    sql.NullInt64
			timeLimitMS   sql.NullInt64
			memoryLimitKB sql.NullInt64
			createdAt     sql.NullTime
		)

		if err := rows.Scan(
			&courseUUID,
			&courseName,
			&groupNamesCSV,
			&groupUUIDsCSV,
			&tasksCount,
			&taskUUID,
			&taskMode,
			&taskTitle,
			&taskText,
			&allowedJSON,
			&testsCount,
			&timeLimitMS,
			&memoryLimitKB,
			&createdAt,
		); err != nil {
			return nil, err
		}

		idx, ok := indexByCourse[courseUUID]
		if !ok {
			groupNames := splitNonEmptyCSV(groupNamesCSV)
			groupUUIDs := splitNonEmptyCSV(groupUUIDsCSV)
			primaryGroupUUID := ""
			if len(groupUUIDs) > 0 {
				primaryGroupUUID = groupUUIDs[0]
			}
			result = append(result, courseOverviewItem{
				CourseUUID: courseUUID,
				CourseName: courseName,
				GroupUUID:  primaryGroupUUID,
				GroupName:  joinStrings(groupNames, ", "),
				GroupUUIDs: groupUUIDs,
				GroupNames: groupNames,
				TasksCount: tasksCount,
				Tasks:      make([]courseTaskItem, 0),
			})
			idx = len(result) - 1
			indexByCourse[courseUUID] = idx
		}

		if taskUUID.Valid {
			item := courseTaskItem{
				TaskUUID:         taskUUID.String,
				TaskMode:         taskMode.String,
				TaskTitle:        taskTitle.String,
				TaskText:         taskText.String,
				AllowedLanguages: allowedLanguagesFromJSON(allowedJSON.String),
				CreatedAt:        createdAt.Time,
			}
			if testsCount.Valid {
				item.TestsCount = int(testsCount.Int64)
			}
			if timeLimitMS.Valid {
				v := timeLimitMS.Int64
				item.TimeLimitMS = &v
			}
			if memoryLimitKB.Valid {
				v := memoryLimitKB.Int64
				item.MemoryLimitKB = &v
			}
			result[idx].Tasks = append(result[idx].Tasks, item)
		}
	}

	return result, nil
}

func teacherCourseListHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET", http.StatusMethodNotAllowed)
		return
	}

	userUUID, userRole, err := getRequestUserInfo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if userRole != "teacher" {
		http.Error(w, "Only teacher can view courses", http.StatusForbidden)
		return
	}

	items, err := loadTeacherCourses(userUUID)
	if err != nil {
		http.Error(w, "Cannot load courses", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(items)
}

func createCourseHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	userUUID, userRole, err := getRequestUserInfo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if userRole != "teacher" {
		http.Error(w, "Only teacher can create course", http.StatusForbidden)
		return
	}

	requestedCourseUUID := strings.TrimSpace(r.FormValue("course_uuid"))
	courseName := strings.TrimSpace(r.FormValue("course_name"))
	groupUUIDs := parseCourseGroupUUIDs(r)

	taskTitle := strings.TrimSpace(r.FormValue("task_title"))
	taskText := strings.TrimSpace(r.FormValue("task_text"))
	taskMode := strings.ToLower(strings.TrimSpace(r.FormValue("task_mode")))
	if taskMode == "" {
		taskMode = "expected"
	}

	if taskTitle == "" || taskText == "" {
		http.Error(w, "Empty required fields", http.StatusBadRequest)
		return
	}
	if taskMode != "tests" && taskMode != "expected" && taskMode != "none" {
		http.Error(w, "Invalid task mode", http.StatusBadRequest)
		return
	}

	var expectedOutput string
	switch taskMode {
	case "expected":
		expectedOutput = strings.TrimSpace(r.FormValue("output_text"))
		if file, _, err := r.FormFile("output_file"); err == nil {
			defer file.Close()

			data, readErr := io.ReadAll(file)
			if readErr != nil {
				http.Error(w, "Cannot read expected output file", http.StatusBadRequest)
				return
			}
			expectedOutput = string(data)
		}
		expectedOutput = canonicalizeText(expectedOutput)
		if expectedOutput == "" {
			http.Error(w, "Expected output is required for this task mode", http.StatusBadRequest)
			return
		}
	case "tests", "none":
		expectedOutput = ""
	}

	inputTexts := r.MultipartForm.Value["test_input_text"]
	outputTexts := r.MultipartForm.Value["test_output_text"]
	visibleValues := r.MultipartForm.Value["test_visible"]

	allowedLanguages := r.Form["allowed_languages"]
	if len(allowedLanguages) == 0 {
		allowedLanguages = []string{"c", "cpp", "go"}
	}

	allowedJSON, err := json.Marshal(allowedLanguages)
	if err != nil {
		http.Error(w, "Cannot encode allowed languages", http.StatusInternalServerError)
		return
	}

	timeLimit := parseOptionalInt(r.FormValue("time_limit_ms"))
	memoryLimit := parseOptionalInt(r.FormValue("memory_limit_kb"))

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Cannot start transaction", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()

	var finalCourseUUID string

	if requestedCourseUUID != "" {
		err = tx.QueryRow(`
			SELECT course_uuid
			FROM courses
			WHERE course_uuid = $1
			  AND teacher_uuid = $2
		`, requestedCourseUUID, userUUID).Scan(&finalCourseUUID)

		if err != nil && err != sql.ErrNoRows {
			http.Error(w, "Cannot load course", http.StatusInternalServerError)
			return
		}
	}

	if finalCourseUUID == "" {
		if courseName == "" || len(groupUUIDs) == 0 {
			http.Error(w, "course_uuid not found, so course_name and at least one group are required to create a new course", http.StatusBadRequest)
			return
		}

		err = tx.QueryRow(`
			INSERT INTO courses (course_name, teacher_uuid)
			VALUES ($1, $2)
			RETURNING course_uuid
		`, courseName, userUUID).Scan(&finalCourseUUID)
		if err != nil {
			http.Error(w, "Cannot create course", http.StatusInternalServerError)
			return
		}

		if err := replaceCourseGroups(tx, finalCourseUUID, groupUUIDs); err != nil {
			http.Error(w, "Cannot save course groups", http.StatusBadRequest)
			return
		}
	}

	var taskUUID string
	err = tx.QueryRow(`
		INSERT INTO tasks (
			course_uuid,
			task_mode,
			task_title,
			task_text,
			result,
			allowed_lang,
			time_limit_ms,
			memory_limit_kb
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING task_uuid
	`,
		finalCourseUUID,
		taskMode,
		taskTitle,
		taskText,
		expectedOutput,
		string(allowedJSON),
		timeLimit,
		memoryLimit,
	).Scan(&taskUUID)
	if err != nil {
		http.Error(w, "Cannot create task", http.StatusInternalServerError)
		return
	}

	testsDir := taskTestsDir(finalCourseUUID, taskUUID)
	savedTests := false
	defer func() {
		if !savedTests {
			_ = os.RemoveAll(testsDir)
		}
	}()

	if taskMode == "tests" {
		count := maxInt(len(inputTexts), len(outputTexts))
		count = maxInt(count, indexedMultipartFieldCount(r.MultipartForm, "test_input_file_"))
		count = maxInt(count, indexedMultipartFieldCount(r.MultipartForm, "test_output_file_"))
		if count == 0 {
			http.Error(w, "Need at least one input/output test pair", http.StatusBadRequest)
			return
		}
		inputData := make([][]byte, count)
		outputData := make([][]byte, count)
		for i := 0; i < count; i++ {
			inputField := fmt.Sprintf("test_input_file_%d", i)
			outputField := fmt.Sprintf("test_output_file_%d", i)

			if data, ok, err := readMultipartFileField(r.MultipartForm, inputField); err != nil {
				http.Error(w, "Cannot read input test file", http.StatusBadRequest)
				return
			} else if ok {
				inputData[i] = data
			} else {
				if i >= len(inputTexts) || strings.TrimSpace(inputTexts[i]) == "" {
					http.Error(w, "Need at least one input/output test pair", http.StatusBadRequest)
					return
				}
				inputData[i] = []byte(inputTexts[i])
			}

			if data, ok, err := readMultipartFileField(r.MultipartForm, outputField); err != nil {
				http.Error(w, "Cannot read output test file", http.StatusBadRequest)
				return
			} else if ok {
				outputData[i] = data
			} else {
				if i >= len(outputTexts) || strings.TrimSpace(outputTexts[i]) == "" {
					http.Error(w, "Need at least one input/output test pair", http.StatusBadRequest)
					return
				}
				outputData[i] = []byte(outputTexts[i])
			}
		}

		visibleFlags := parseVisibilityFlags(visibleValues, count)
		if err := saveTaskTestsData(tx, finalCourseUUID, taskUUID, inputData, outputData, visibleFlags); err != nil {
			http.Error(w, "Cannot save task tests", http.StatusInternalServerError)
			return
		}
		savedTests = true
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "Cannot save course", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"course_uuid": finalCourseUUID,
		"task_uuid":   taskUUID,
	})
}

func teacherUpdateCourseHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	userUUID, userRole, err := getRequestUserInfo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if userRole != "teacher" {
		http.Error(w, "Only teacher can update course", http.StatusForbidden)
		return
	}

	courseUUID := strings.TrimSpace(r.FormValue("course_uuid"))
	courseName := strings.TrimSpace(r.FormValue("course_name"))
	groupUUIDs := parseCourseGroupUUIDs(r)
	if courseUUID == "" || courseName == "" || len(groupUUIDs) == 0 {
		http.Error(w, "missing course_uuid, course_name or at least one group", http.StatusBadRequest)
		return
	}

	var owned string
	if err := db.QueryRow(`
		SELECT course_uuid
		FROM courses
		WHERE course_uuid = $1
		  AND teacher_uuid = $2
	`, courseUUID, userUUID).Scan(&owned); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Course not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load course", http.StatusInternalServerError)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Cannot start transaction", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		UPDATE courses
		SET course_name = $1
		WHERE course_uuid = $2
		  AND teacher_uuid = $3
	`, courseName, courseUUID, userUUID); err != nil {
		http.Error(w, "Cannot update course", http.StatusInternalServerError)
		return
	}

	if err := replaceCourseGroups(tx, courseUUID, groupUUIDs); err != nil {
		http.Error(w, "Cannot save course groups", http.StatusBadRequest)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "Cannot save course", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"course_uuid": courseUUID,
		"course_name": courseName,
		"group_uuids": groupUUIDs,
		"status":      "updated",
	})
}

func teacherTaskEditDataHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET", http.StatusMethodNotAllowed)
		return
	}

	userUUID, userRole, err := getRequestUserInfo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if userRole != "teacher" {
		http.Error(w, "Only teacher can view task", http.StatusForbidden)
		return
	}

	courseUUID := strings.TrimSpace(r.FormValue("course_uuid"))
	taskUUID := strings.TrimSpace(r.FormValue("task_uuid"))
	if courseUUID == "" || taskUUID == "" {
		http.Error(w, "missing course_uuid or task_uuid", http.StatusBadRequest)
		return
	}

	var resp teacherTaskEditResponse
	var allowedJSON string
	var groupUUIDCSV string
	var timeLimitMS sql.NullInt64
	var memoryLimitKB sql.NullInt64

	err = db.QueryRow(`
		SELECT
			c.course_uuid,
			c.course_name,
			COALESCE(string_agg(g.group_uuid::text, ',' ORDER BY g.group_name), '') AS group_uuids,
			COALESCE(string_agg(g.group_name, ', ' ORDER BY g.group_name), '') AS group_name,
			t.task_uuid,
			t.task_mode,
			t.task_title,
			t.task_text,
			t.result,
			t.allowed_lang,
			COALESCE((SELECT COUNT(*) FROM task_tests x WHERE x.task_uuid = t.task_uuid), 0) AS tests_count,
			t.time_limit_ms,
			t.memory_limit_kb
		FROM courses c
		LEFT JOIN course_groups cg ON cg.course_uuid = c.course_uuid
		LEFT JOIN student_groups g ON g.group_uuid = cg.group_uuid
		JOIN tasks t ON t.course_uuid = c.course_uuid
		WHERE c.course_uuid = $1
		  AND c.teacher_uuid = $2
		  AND t.task_uuid = $3
		GROUP BY c.course_uuid, c.course_name, t.task_uuid, t.task_mode, t.task_title, t.task_text, t.result, t.allowed_lang, t.time_limit_ms, t.memory_limit_kb
		LIMIT 1
	`, courseUUID, userUUID, taskUUID).Scan(
		&resp.CourseUUID,
		&resp.CourseName,
		&groupUUIDCSV,
		&resp.GroupName,
		&resp.TaskUUID,
		&resp.TaskMode,
		&resp.TaskTitle,
		&resp.TaskText,
		&resp.ExpectedOutput,
		&allowedJSON,
		&resp.TestsCount,
		&timeLimitMS,
		&memoryLimitKB,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load task", http.StatusInternalServerError)
		return
	}

	resp.AllowedLanguages = allowedLanguagesFromJSON(allowedJSON)
	if timeLimitMS.Valid {
		v := timeLimitMS.Int64
		resp.TimeLimitMS = &v
	}
	if memoryLimitKB.Valid {
		v := memoryLimitKB.Int64
		resp.MemoryLimitKB = &v
	}

	if strings.ToLower(strings.TrimSpace(resp.TaskMode)) == "tests" {
		tests, err := loadTaskTestDetails(resp.CourseUUID, resp.TaskUUID)
		if err != nil {
			http.Error(w, "Cannot load task tests", http.StatusInternalServerError)
			return
		}
		resp.Tests = tests
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func teacherUpdateTaskHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	userUUID, userRole, err := getRequestUserInfo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if userRole != "teacher" {
		http.Error(w, "Only teacher can update task", http.StatusForbidden)
		return
	}

	sourceCourseUUID := strings.TrimSpace(r.FormValue("source_course_uuid"))
	targetCourseUUID := strings.TrimSpace(r.FormValue("course_uuid"))
	taskUUID := strings.TrimSpace(r.FormValue("task_uuid"))
	if sourceCourseUUID == "" || taskUUID == "" {
		http.Error(w, "missing source_course_uuid or task_uuid", http.StatusBadRequest)
		return
	}
	if targetCourseUUID == "" {
		targetCourseUUID = sourceCourseUUID
	}

	taskTitle := strings.TrimSpace(r.FormValue("task_title"))
	taskText := strings.TrimSpace(r.FormValue("task_text"))
	taskMode := strings.ToLower(strings.TrimSpace(r.FormValue("task_mode")))
	if taskMode == "" {
		taskMode = "expected"
	}
	if taskTitle == "" || taskText == "" {
		http.Error(w, "Empty required fields", http.StatusBadRequest)
		return
	}
	if taskMode != "tests" && taskMode != "expected" && taskMode != "none" {
		http.Error(w, "Invalid task mode", http.StatusBadRequest)
		return
	}

	var sourceOwned, targetOwned string
	if err := db.QueryRow(`
		SELECT c.course_uuid
		FROM courses c
		JOIN tasks t ON t.course_uuid = c.course_uuid
		WHERE c.course_uuid = $1
		  AND c.teacher_uuid = $2
		  AND t.task_uuid = $3
	`, sourceCourseUUID, userUUID, taskUUID).Scan(&sourceOwned); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load task", http.StatusInternalServerError)
		return
	}
	if err := db.QueryRow(`
		SELECT course_uuid
		FROM courses
		WHERE course_uuid = $1
		  AND teacher_uuid = $2
	`, targetCourseUUID, userUUID).Scan(&targetOwned); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Target course not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load target course", http.StatusInternalServerError)
		return
	}

	var expectedOutput string
	switch taskMode {
	case "expected":
		expectedOutput = strings.TrimSpace(r.FormValue("output_text"))
		if file, _, err := r.FormFile("output_file"); err == nil {
			defer file.Close()
			data, readErr := io.ReadAll(file)
			if readErr != nil {
				http.Error(w, "Cannot read expected output file", http.StatusBadRequest)
				return
			}
			expectedOutput = string(data)
		}
		expectedOutput = canonicalizeText(expectedOutput)
		if expectedOutput == "" {
			http.Error(w, "Expected output is required for this task mode", http.StatusBadRequest)
			return
		}
	case "tests", "none":
		expectedOutput = ""
	}

	inputTexts := r.MultipartForm.Value["test_input_text"]
	outputTexts := r.MultipartForm.Value["test_output_text"]
	visibleValues := r.MultipartForm.Value["test_visible"]
	if taskMode == "tests" {
		count := maxInt(len(inputTexts), len(outputTexts))
		if count == 0 {
			http.Error(w, "Need at least one input/output test pair", http.StatusBadRequest)
			return
		}
		for i := 0; i < count; i++ {
			inputField := fmt.Sprintf("test_input_file_%d", i)
			outputField := fmt.Sprintf("test_output_file_%d", i)

			inputData, inputHasFile, err := readMultipartFileField(r.MultipartForm, inputField)
			if err != nil {
				http.Error(w, "Cannot read input test file", http.StatusBadRequest)
				return
			}
			if !inputHasFile {
				if i >= len(inputTexts) || strings.TrimSpace(inputTexts[i]) == "" {
					http.Error(w, "Need at least one input/output test pair", http.StatusBadRequest)
					return
				}
				inputData = []byte(inputTexts[i])
			}

			outputData, outputHasFile, err := readMultipartFileField(r.MultipartForm, outputField)
			if err != nil {
				http.Error(w, "Cannot read output test file", http.StatusBadRequest)
				return
			}
			if !outputHasFile {
				if i >= len(outputTexts) || strings.TrimSpace(outputTexts[i]) == "" {
					http.Error(w, "Need at least one input/output test pair", http.StatusBadRequest)
					return
				}
				outputData = []byte(outputTexts[i])
			}

			_ = inputData
			_ = outputData
		}
	}

	allowedLanguages := r.Form["allowed_languages"]
	if len(allowedLanguages) == 0 {
		allowedLanguages = []string{"c", "cpp", "go"}
	}
	allowedJSON, err := json.Marshal(allowedLanguages)
	if err != nil {
		http.Error(w, "Cannot encode allowed languages", http.StatusInternalServerError)
		return
	}

	timeLimit := parseOptionalInt(r.FormValue("time_limit_ms"))
	memoryLimit := parseOptionalInt(r.FormValue("memory_limit_kb"))

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Cannot start transaction", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		UPDATE tasks
		SET course_uuid = $1,
		    task_mode = $2,
		    task_title = $3,
		    task_text = $4,
		    result = $5,
		    allowed_lang = $6,
		    time_limit_ms = $7,
		    memory_limit_kb = $8
		WHERE task_uuid = $9
		  AND course_uuid = $10
	`, targetCourseUUID, taskMode, taskTitle, taskText, expectedOutput, string(allowedJSON), timeLimit, memoryLimit, taskUUID, sourceCourseUUID); err != nil {
		http.Error(w, "Cannot update task", http.StatusInternalServerError)
		return
	}

	if _, err := tx.Exec(`DELETE FROM task_tests WHERE task_uuid = $1`, taskUUID); err != nil {
		http.Error(w, "Cannot clear task tests", http.StatusInternalServerError)
		return
	}

	if taskMode == "tests" {
		count := maxInt(len(inputTexts), len(outputTexts))
		count = maxInt(count, indexedMultipartFieldCount(r.MultipartForm, "test_input_file_"))
		count = maxInt(count, indexedMultipartFieldCount(r.MultipartForm, "test_output_file_"))
		if count == 0 {
			http.Error(w, "Need at least one input/output test pair", http.StatusBadRequest)
			return
		}
		inputData := make([][]byte, count)
		outputData := make([][]byte, count)
		for i := 0; i < count; i++ {
			inputField := fmt.Sprintf("test_input_file_%d", i)
			outputField := fmt.Sprintf("test_output_file_%d", i)

			if data, ok, err := readMultipartFileField(r.MultipartForm, inputField); err != nil {
				http.Error(w, "Cannot read input test file", http.StatusBadRequest)
				return
			} else if ok {
				inputData[i] = data
			} else {
				inputData[i] = []byte(inputTexts[i])
			}

			if data, ok, err := readMultipartFileField(r.MultipartForm, outputField); err != nil {
				http.Error(w, "Cannot read output test file", http.StatusBadRequest)
				return
			} else if ok {
				outputData[i] = data
			} else {
				outputData[i] = []byte(outputTexts[i])
			}
		}

		visibleFlags := parseVisibilityFlags(visibleValues, count)
		if err := saveTaskTestsData(tx, targetCourseUUID, taskUUID, inputData, outputData, visibleFlags); err != nil {
			http.Error(w, "Cannot save task tests", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "Cannot save task", http.StatusInternalServerError)
		return
	}

	if sourceCourseUUID != targetCourseUUID {
		_ = os.RemoveAll(taskTestsDir(sourceCourseUUID, taskUUID))
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"course_uuid": targetCourseUUID,
		"task_uuid":   taskUUID,
		"status":      "updated",
	})
}

func removeTeacherCourseStorage(courseUUID string) {
	_ = os.RemoveAll(filepath.Join(testsRoot, courseUUID))
	_ = os.RemoveAll(filepath.Join(storageRoot, courseUUID))
}

func removeTeacherTaskStorage(courseUUID, taskUUID string) {
	_ = os.RemoveAll(taskTestsDir(courseUUID, taskUUID))
	_ = os.RemoveAll(filepath.Join(storageRoot, courseUUID, taskUUID))
}

func teacherDeleteCourseHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	userUUID, userRole, err := getRequestUserInfo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if userRole != "teacher" {
		http.Error(w, "Only teacher can delete courses", http.StatusForbidden)
		return
	}

	courseUUID := strings.TrimSpace(r.FormValue("course_uuid"))
	if courseUUID == "" {
		http.Error(w, "missing course_uuid", http.StatusBadRequest)
		return
	}

	var owned string
	if err := db.QueryRow(`
		SELECT course_uuid
		FROM courses
		WHERE course_uuid = $1
		  AND teacher_uuid = $2
	`, courseUUID, userUUID).Scan(&owned); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Course not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load course", http.StatusInternalServerError)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Cannot start transaction", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		DELETE FROM submissions
		WHERE course_uuid = $1
	`, courseUUID); err != nil {
		http.Error(w, "Cannot delete course", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`
		DELETE FROM task_tests
		WHERE task_uuid IN (
			SELECT task_uuid FROM tasks WHERE course_uuid = $1
		)
	`, courseUUID); err != nil {
		http.Error(w, "Cannot delete course", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`
		DELETE FROM tasks
		WHERE course_uuid = $1
	`, courseUUID); err != nil {
		http.Error(w, "Cannot delete course", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`
		DELETE FROM courses
		WHERE course_uuid = $1
		  AND teacher_uuid = $2
	`, courseUUID, userUUID); err != nil {
		http.Error(w, "Cannot delete course", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "Cannot delete course", http.StatusInternalServerError)
		return
	}

	removeTeacherCourseStorage(courseUUID)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"course_uuid": courseUUID,
		"status":      "deleted",
	})
}

func teacherDeleteTaskHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	userUUID, userRole, err := getRequestUserInfo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if userRole != "teacher" {
		http.Error(w, "Only teacher can delete tasks", http.StatusForbidden)
		return
	}

	courseUUID := strings.TrimSpace(r.FormValue("course_uuid"))
	taskUUID := strings.TrimSpace(r.FormValue("task_uuid"))
	if courseUUID == "" || taskUUID == "" {
		http.Error(w, "missing course_uuid or task_uuid", http.StatusBadRequest)
		return
	}

	var ownedCourse string
	if err := db.QueryRow(`
		SELECT c.course_uuid
		FROM courses c
		JOIN tasks t ON t.course_uuid = c.course_uuid
		WHERE c.course_uuid = $1
		  AND c.teacher_uuid = $2
		  AND t.task_uuid = $3
	`, courseUUID, userUUID, taskUUID).Scan(&ownedCourse); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Task not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load task", http.StatusInternalServerError)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Cannot start transaction", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		DELETE FROM submissions
		WHERE course_uuid = $1
		  AND task_uuid = $2
	`, courseUUID, taskUUID); err != nil {
		http.Error(w, "Cannot delete task", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`
		DELETE FROM task_tests
		WHERE task_uuid = $1
	`, taskUUID); err != nil {
		http.Error(w, "Cannot delete task", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`
		DELETE FROM tasks
		WHERE task_uuid = $1
		  AND course_uuid = $2
	`, taskUUID, courseUUID); err != nil {
		http.Error(w, "Cannot delete task", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "Cannot delete task", http.StatusInternalServerError)
		return
	}

	removeTeacherTaskStorage(courseUUID, taskUUID)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"course_uuid": courseUUID,
		"task_uuid":   taskUUID,
		"status":      "deleted",
	})
}

func teacherCoursesHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET", http.StatusMethodNotAllowed)
		return
	}

	userUUID, userRole, err := getRequestUserInfo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if userRole != "teacher" {
		http.Error(w, "Only teacher can view courses", http.StatusForbidden)
		return
	}

	items, err := loadTeacherCourses(userUUID)
	if err != nil {
		http.Error(w, "Cannot load courses", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(items)
}

func courseResultsHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET", http.StatusMethodNotAllowed)
		return
	}

	userUUID, userRole, err := getRequestUserInfo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if userRole != "teacher" {
		http.Error(w, "Only teacher can view results", http.StatusForbidden)
		return
	}

	courseUUID := strings.TrimSpace(r.FormValue("course_uuid"))
	taskUUID := strings.TrimSpace(r.FormValue("task_uuid"))
	if courseUUID == "" {
		http.Error(w, "missing course_uuid", http.StatusBadRequest)
		return
	}

	resp := teacherTaskResultsResponse{Submissions: make([]teacherSubmissionItem, 0), CourseGroups: make([]groupItem, 0)}

	var allowedJSON string
	var timeLimitMS sql.NullInt64
	var memoryLimitKB sql.NullInt64

	task, err := getTaskByCourse(courseUUID, taskUUID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Course/task not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load task", http.StatusInternalServerError)
		return
	}

	err = db.QueryRow(`
		SELECT
			c.course_uuid,
			c.course_name,
			COALESCE(string_agg(g.group_name, ', ' ORDER BY g.group_name), '') AS group_names,
			t.task_uuid,
			t.task_mode,
			t.task_title,
			t.task_text,
			t.result,
			t.allowed_lang,
			COALESCE((SELECT COUNT(*) FROM task_tests tt WHERE tt.task_uuid = t.task_uuid), 0) AS tests_count,
			t.time_limit_ms,
			t.memory_limit_kb
		FROM courses c
		LEFT JOIN course_groups cg ON cg.course_uuid = c.course_uuid
		LEFT JOIN student_groups g ON g.group_uuid = cg.group_uuid
		JOIN tasks t ON t.task_uuid = $1
		WHERE c.course_uuid = $2
		  AND c.teacher_uuid = $3
		GROUP BY c.course_uuid, c.course_name, t.task_uuid, t.task_mode, t.task_title, t.task_text, t.result, t.allowed_lang, t.time_limit_ms, t.memory_limit_kb
		LIMIT 1
	`, task.TaskUUID, courseUUID, userUUID).Scan(
		&resp.CourseUUID,
		&resp.CourseName,
		&resp.GroupName,
		&resp.TaskUUID,
		&resp.TaskMode,
		&resp.TaskTitle,
		&resp.TaskText,
		&resp.ExpectedOutput,
		&allowedJSON,
		&resp.TestsCount,
		&timeLimitMS,
		&memoryLimitKB,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Course not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load course", http.StatusInternalServerError)
		return
	}

	resp.AllowedLanguages = allowedLanguagesFromJSON(allowedJSON)
	if timeLimitMS.Valid {
		v := timeLimitMS.Int64
		resp.TimeLimitMS = &v
	}
	if memoryLimitKB.Valid {
		v := memoryLimitKB.Int64
		resp.MemoryLimitKB = &v
	}

	groupRows, err := db.Query(`
		SELECT
			g.group_uuid::text,
			g.group_prefix,
			g.group_number,
			g.group_name
		FROM course_groups cg
		JOIN student_groups g ON g.group_uuid = cg.group_uuid
		WHERE cg.course_uuid = $1
		ORDER BY g.group_name
	`, resp.CourseUUID)
	if err != nil {
		http.Error(w, "Cannot load course groups", http.StatusInternalServerError)
		return
	}
	for groupRows.Next() {
		var item groupItem
		if err := groupRows.Scan(&item.UUID, &item.Prefix, &item.Number, &item.Name); err != nil {
			groupRows.Close()
			http.Error(w, "Cannot read course groups", http.StatusInternalServerError)
			return
		}
		resp.CourseGroups = append(resp.CourseGroups, item)
	}
	groupRows.Close()

	rows, err := db.Query(`
		WITH latest_submissions AS (
			SELECT DISTINCT ON (s.student_uuid)
				s.submission_uuid,
				s.student_uuid,
				COALESCE(st.last_name, '') || ' ' || COALESCE(st.first_name, '') ||
					CASE
						WHEN st.middle_name IS NOT NULL AND TRIM(st.middle_name) <> '' THEN ' ' || TRIM(st.middle_name)
						ELSE ''
					END AS student_name,
				COALESCE(st.login, '') AS student_login,
				COALESCE(st.group_uuid::text, '') AS student_group_uuid,
				COALESCE(sg.group_name, '') AS student_group_name,
				s.lang,
				s.lang_version,
				s.source_path,
				s.source_hash,
				s.checker_output,
				s.created_at,
				s.updated_at
			FROM submissions s
			LEFT JOIN students st ON st.student_uuid = s.student_uuid
			LEFT JOIN student_groups sg ON sg.group_uuid = st.group_uuid
			WHERE s.course_uuid = $1
			  AND s.task_uuid = $2
			ORDER BY s.student_uuid, s.updated_at DESC, s.created_at DESC, s.submission_uuid DESC
		), attempt_counts AS (
			SELECT student_uuid, COUNT(*) AS attempts_count, MAX(created_at) AS last_attempt_at
			FROM submission_attempts
			WHERE course_uuid = $1
			  AND task_uuid = $2
			GROUP BY student_uuid
		)
		SELECT
			ls.submission_uuid,
			ls.student_uuid,
			ls.student_name,
			ls.student_login,
			ls.student_group_uuid,
			ls.student_group_name,
			ls.lang,
			ls.lang_version,
			ls.source_path,
			ls.source_hash,
			ls.checker_output,
			ls.created_at,
			ls.updated_at,
			COALESCE(ac.attempts_count, 0),
			ac.last_attempt_at
		FROM latest_submissions ls
		LEFT JOIN attempt_counts ac ON ac.student_uuid = ls.student_uuid
		ORDER BY ls.updated_at DESC, ls.student_name ASC, ls.student_login ASC
	`, resp.CourseUUID, resp.TaskUUID)
	if err != nil {
		http.Error(w, "Cannot load submissions", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var item teacherSubmissionItem
		var lastAttemptAt sql.NullTime
		if err := rows.Scan(
			&item.SubmissionUUID,
			&item.StudentUUID,
			&item.StudentName,
			&item.StudentLogin,
			&item.StudentGroupUUID,
			&item.StudentGroupName,
			&item.Language,
			&item.LanguageVersion,
			&item.SourcePath,
			&item.SourceHash,
			&item.CheckerOutput,
			&item.CreatedAt,
			&item.UpdatedAt,
			&item.AttemptsCount,
			&lastAttemptAt,
		); err != nil {
			http.Error(w, "Cannot read submissions", http.StatusInternalServerError)
			return
		}
		if lastAttemptAt.Valid {
			v := lastAttemptAt.Time
			item.LastAttemptAt = &v
		}
		resp.Submissions = append(resp.Submissions, item)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func courseSubmissionHistoryHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET", http.StatusMethodNotAllowed)
		return
	}

	userUUID, userRole, err := getRequestUserInfo(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if userRole != "teacher" {
		http.Error(w, "Only teacher can view history", http.StatusForbidden)
		return
	}

	courseUUID := strings.TrimSpace(r.FormValue("course_uuid"))
	taskUUID := strings.TrimSpace(r.FormValue("task_uuid"))
	studentUUID := strings.TrimSpace(r.FormValue("student_uuid"))
	if courseUUID == "" || taskUUID == "" || studentUUID == "" {
		http.Error(w, "missing course_uuid, task_uuid or student_uuid", http.StatusBadRequest)
		return
	}

	resp := teacherTaskHistoryResponse{Attempts: make([]teacherSubmissionHistoryItem, 0)}

	var allowedJSON string
	var timeLimitMS sql.NullInt64
	var memoryLimitKB sql.NullInt64

	task, err := getTaskByCourse(courseUUID, taskUUID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Course/task not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load task", http.StatusInternalServerError)
		return
	}

	err = db.QueryRow(`
		SELECT
			c.course_uuid,
			c.course_name,
			COALESCE(string_agg(g.group_name, ', ' ORDER BY g.group_name), '') AS group_names,
			t.task_uuid,
			t.task_mode,
			t.task_title,
			t.task_text,
			t.result,
			t.allowed_lang,
			COALESCE((SELECT COUNT(*) FROM task_tests tt WHERE tt.task_uuid = t.task_uuid), 0) AS tests_count,
			t.time_limit_ms,
			t.memory_limit_kb
		FROM courses c
		LEFT JOIN course_groups cg ON cg.course_uuid = c.course_uuid
		LEFT JOIN student_groups g ON g.group_uuid = cg.group_uuid
		JOIN tasks t ON t.task_uuid = $1
		WHERE c.course_uuid = $2
		  AND c.teacher_uuid = $3
		GROUP BY c.course_uuid, c.course_name, t.task_uuid, t.task_mode, t.task_title, t.task_text, t.result, t.allowed_lang, t.time_limit_ms, t.memory_limit_kb
		LIMIT 1
	`, task.TaskUUID, courseUUID, userUUID).Scan(
		&resp.CourseUUID,
		&resp.CourseName,
		&resp.GroupName,
		&resp.TaskUUID,
		&resp.TaskMode,
		&resp.TaskTitle,
		&resp.TaskText,
		&resp.ExpectedOutput,
		&allowedJSON,
		&resp.TestsCount,
		&timeLimitMS,
		&memoryLimitKB,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Course not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load course", http.StatusInternalServerError)
		return
	}

	resp.AllowedLanguages = allowedLanguagesFromJSON(allowedJSON)
	if timeLimitMS.Valid {
		v := timeLimitMS.Int64
		resp.TimeLimitMS = &v
	}
	if memoryLimitKB.Valid {
		v := memoryLimitKB.Int64
		resp.MemoryLimitKB = &v
	}

	if err := db.QueryRow(`
		SELECT
			COALESCE(st.last_name, '') || ' ' || COALESCE(st.first_name, '') ||
				CASE
					WHEN st.middle_name IS NOT NULL AND TRIM(st.middle_name) <> '' THEN ' ' || TRIM(st.middle_name)
					ELSE ''
				END AS student_name,
			COALESCE(st.login, '')
		FROM submission_attempts a
		JOIN students st ON st.student_uuid = a.student_uuid
		WHERE a.course_uuid = $1
		  AND a.task_uuid = $2
		  AND a.student_uuid = $3
		LIMIT 1
	`, resp.CourseUUID, resp.TaskUUID, studentUUID).Scan(&resp.StudentName, &resp.StudentLogin); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Student not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Cannot load student", http.StatusInternalServerError)
		return
	}
	resp.StudentUUID = studentUUID

	rows, err := db.Query(`
		SELECT
			a.attempt_uuid,
			a.submission_uuid,
			a.lang,
			a.lang_version,
			a.source_path,
			a.source_hash,
			a.checker_output,
			a.created_at,
			a.updated_at,
			ROW_NUMBER() OVER (ORDER BY a.created_at ASC, a.updated_at ASC, a.attempt_uuid ASC) AS attempt_number
		FROM submission_attempts a
		WHERE a.course_uuid = $1
		  AND a.task_uuid = $2
		  AND a.student_uuid = $3
		ORDER BY a.created_at DESC, a.updated_at DESC, a.attempt_uuid DESC
	`, resp.CourseUUID, resp.TaskUUID, studentUUID)
	if err != nil {
		http.Error(w, "Cannot load history", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var item teacherSubmissionHistoryItem
		if err := rows.Scan(
			&item.AttemptUUID,
			&item.SubmissionUUID,
			&item.Language,
			&item.LanguageVersion,
			&item.SourcePath,
			&item.SourceHash,
			&item.CheckerOutput,
			&item.CreatedAt,
			&item.UpdatedAt,
			&item.AttemptNumber,
		); err != nil {
			http.Error(w, "Cannot read history", http.StatusInternalServerError)
			return
		}
		resp.Attempts = append(resp.Attempts, item)
	}
	resp.AttemptsCount = len(resp.Attempts)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}
