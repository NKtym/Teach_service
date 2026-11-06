package main

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var db *sql.DB

func initDB() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://pavel:1234554321@postgres:5432/code_checker?sslmode=disable"
	}

	var lastErr error

	for i := 0; i < 30; i++ {
		conn, err := sql.Open("pgx", dsn)
		if err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}

		err = conn.Ping()
		if err == nil {
			db = conn
			return nil
		}

		lastErr = err
		_ = conn.Close()
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("cannot connect to db: %w", lastErr)
}

func closeDB() {
	if db != nil {
		_ = db.Close()
	}
}
