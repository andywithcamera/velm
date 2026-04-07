package db

import (
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var Pool *pgxpool.Pool

type UUID = string

type SingleRow struct {
	View *View
	Data any
}

type MultiRow struct {
	View *View
	Data []any
}

type Table struct {
	ID             UUID
	NAME           string
	CREATED_AT     time.Time
	CREATED_BY     UUID
	UPDATED_AT     time.Time
	UPDATED_BY     UUID
	LABEL_SINGULAR string
	LABEL_PLURAL   string
	DESCRIPTION    string
	DISPLAY_FIELD  string
}

type ChoiceOption struct {
	Value string `yaml:"value" json:"value"`
	Label string `yaml:"label" json:"label"`
}

type Column struct {
	ID               string
	NAME             string
	CREATED_AT       time.Time
	CREATED_BY       string
	UPDATED_AT       time.Time
	UPDATED_BY       string
	LABEL            string
	DATA_TYPE        string
	IS_NULLABLE      bool
	DEFAULT_VALUE    sql.NullString
	IS_HIDDEN        bool
	IS_READONLY      bool
	VALIDATION_REGEX sql.NullString
	VALIDATION_EXPR  sql.NullString
	CONDITION_EXPR   sql.NullString
	VALIDATION_MSG   sql.NullString
	REFERENCE_TABLE  sql.NullString
	PREFIX           sql.NullString
	CHOICES          []ChoiceOption
	TABLE_ID         string
}

type View struct {
	Table   *Table
	Columns []Column
}
