package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

func loadStudentCourses(userUUID string) ([]courseOverviewItem, error) {
	rows, err := db.Query(`
		WITH accessible_courses AS (
			SELECT
				c.course_uuid,
				c.course_name,
				COALESCE(string_agg(g.group_name, ', ' ORDER BY g.group_name), '') AS group_names,
				COALESCE(string_agg(g.group_uuid::text, ',' ORDER BY g.group_name), '') AS group_uuids,
				COALESCE((SELECT COUNT(*) FROM tasks tt WHERE tt.course_uuid = c.course_uuid), 0) AS tasks_count,
				c.created_at
			FROM courses c
			JOIN course_groups cg ON cg.course_uuid = c.course_uuid
			JOIN student_groups g ON g.group_uuid = cg.group_uuid
			JOIN students s ON s.student_uuid = $1 AND s.group_uuid = cg.group_uuid
			GROUP BY c.course_uuid, c.course_name, c.created_at
		)
		SELECT
			ac.course_uuid,
			ac.course_name,
			ac.group_names,
			ac.group_uuids,
			ac.tasks_count,
			t.task_uuid,
			t.task_mode,
			t.task_title,
			t.task_text,
			t.allowed_lang,
			COALESCE((SELECT COUNT(*) FROM task_tests x WHERE x.task_uuid = t.task_uuid), 0) AS tests_count,
			t.time_limit_ms,
			t.memory_limit_kb,
			t.created_at
		FROM accessible_courses ac
		LEFT JOIN tasks t ON t.course_uuid = ac.course_uuid
		ORDER BY ac.created_at DESC, t.created_at DESC NULLS LAST
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

func listStudentCoursesHandler(w http.ResponseWriter, r *http.Request) {
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
	if userRole != "student" {
		http.Error(w, "Only student can view courses", http.StatusForbidden)
		return
	}

	items, err := loadStudentCourses(userUUID)
	if err != nil {
		http.Error(w, "Cannot load courses", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(items)
}

func studentCourseHandler(w http.ResponseWriter, r *http.Request) {
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
	if userRole != "student" {
		http.Error(w, "Only student can view course", http.StatusForbidden)
		return
	}

	courseUUID := r.URL.Query().Get("course_uuid")
	if courseUUID == "" {
		http.Error(w, "missing course_uuid", http.StatusBadRequest)
		return
	}

	items, err := loadStudentCourses(userUUID)
	if err != nil {
		http.Error(w, "Cannot load courses", http.StatusInternalServerError)
		return
	}

	for _, item := range items {
		if item.CourseUUID == courseUUID {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(item)
			return
		}
	}

	http.Error(w, "Course not found", http.StatusNotFound)
}
