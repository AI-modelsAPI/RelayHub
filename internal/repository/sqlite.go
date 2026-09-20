package repository

import "database/sql"

type SQLiteStore struct{ *Store }

func NewSQLite(db *sql.DB) *SQLiteStore { return &SQLiteStore{Store: New(db)} }
