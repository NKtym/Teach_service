package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type loginResponse struct {
	Role string `json:"role"`
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type authUser struct {
	UUID         string
	LastName     string
	FirstName    string
	MiddleName   sql.NullString
	PasswordHash string
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", http.StatusMethodNotAllowed)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	req.Login = strings.TrimSpace(req.Login)
	req.Password = strings.TrimSpace(req.Password)

	if req.Login == "" || req.Password == "" {
		http.Error(w, "Empty login or password", http.StatusBadRequest)
		return
	}

	role, user, err := findUserByLogin(req.Login, req.Password)
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	resp := loginResponse{
		Role: role,
		UUID: user.UUID,
		Name: buildFullName(user.LastName, user.FirstName, user.MiddleName),
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func findUserByLogin(login, password string) (string, *authUser, error) {
	user := authUser{}

	err := db.QueryRow(`
		SELECT student_uuid, last_name, first_name, middle_name, password_hash
		FROM students
		WHERE login = $1
	`, login).Scan(&user.UUID, &user.LastName, &user.FirstName, &user.MiddleName, &user.PasswordHash)

	if err == nil {
		if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) == nil {
			return "student", &user, nil
		}
		return "", nil, fmt.Errorf("invalid credentials")
	}

	if err != sql.ErrNoRows {
		return "", nil, err
	}

	err = db.QueryRow(`
		SELECT teacher_uuid, last_name, first_name, middle_name, password_hash
		FROM teachers
		WHERE login = $1
	`, login).Scan(&user.UUID, &user.LastName, &user.FirstName, &user.MiddleName, &user.PasswordHash)

	if err == nil {
		if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) == nil {
			return "teacher", &user, nil
		}
		return "", nil, fmt.Errorf("invalid credentials")
	}

	return "", nil, fmt.Errorf("invalid credentials")
}

func buildFullName(lastName, firstName string, middleName sql.NullString) string {
	if middleName.Valid && strings.TrimSpace(middleName.String) != "" {
		return lastName + " " + firstName + " " + middleName.String
	}
	return lastName + " " + firstName
}
