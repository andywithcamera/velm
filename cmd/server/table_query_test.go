package main

import (
	"strings"
	"testing"
)

func TestBuildTableWhereClausePlainFallback(t *testing.T) {
	cols := []tableQueryColumn{
		{Name: "Name", DataType: "text"},
		{Name: "Age", DataType: "int", IsNumber: true},
	}
	quoted := map[string]string{
		"Name": `"Name"`,
		"Age":  `"Age"`,
	}

	sql, args, structured, err := buildTableWhereClause("Andy", cols, quoted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if structured {
		t.Fatal("expected plain-text fallback")
	}
	if !strings.Contains(sql, `CAST("Name" AS TEXT) ILIKE $1`) {
		t.Fatalf("unexpected sql: %s", sql)
	}
	if len(args) != 1 || args[0] != "%Andy%" {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestBuildTableWhereClauseStructured(t *testing.T) {
	cols := []tableQueryColumn{
		{Name: "Name", Label: "Name", DataType: "text"},
		{Name: "Age", Label: "Age", DataType: "int", IsNumber: true},
	}
	quoted := map[string]string{
		"Name": `"Name"`,
		"Age":  `"Age"`,
	}

	sql, args, structured, err := buildTableWhereClause(`Name=Andy* && Age < 50 || Name=*Doyle*`, cols, quoted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !structured {
		t.Fatal("expected structured query mode")
	}
	if !strings.Contains(sql, `CAST("Name" AS TEXT) ILIKE`) {
		t.Fatalf("expected wildcard text comparison in sql: %s", sql)
	}
	if !strings.Contains(sql, `"Age" <`) {
		t.Fatalf("expected numeric comparison in sql: %s", sql)
	}
	if len(args) != 3 {
		t.Fatalf("unexpected arg count: %#v", args)
	}
}

func TestBuildTableWhereClauseStructuredWithLabels(t *testing.T) {
	cols := []tableQueryColumn{
		{Name: "_created_by", Label: "Created By", DataType: "text"},
		{Name: "_created_at", Label: "Created On", DataType: "timestamp", IsDateLike: true},
	}
	quoted := map[string]string{
		"_created_by": `"_created_by"`,
		"_created_at": `"_created_at"`,
	}

	sql, args, structured, err := buildTableWhereClause(`Created By=Andy* && Created On > 2026-03-01`, cols, quoted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !structured {
		t.Fatal("expected structured query mode")
	}
	if !strings.Contains(sql, `CAST("_created_by" AS TEXT) ILIKE`) {
		t.Fatalf("expected label alias to resolve to _created_by: %s", sql)
	}
	if !strings.Contains(sql, `"_created_at" >`) {
		t.Fatalf("expected label alias to resolve to _created_at: %s", sql)
	}
	if len(args) != 2 {
		t.Fatalf("unexpected arg count: %#v", args)
	}
}

func TestBuildTableWhereClauseDateComparison(t *testing.T) {
	cols := []tableQueryColumn{
		{Name: "Created_At", DataType: "timestamp", IsDateLike: true},
	}
	quoted := map[string]string{
		"Created_At": `"Created_At"`,
	}

	sql, args, structured, err := buildTableWhereClause(`Created_At > 2026-03-01`, cols, quoted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !structured {
		t.Fatal("expected structured query mode")
	}
	if !strings.Contains(sql, `"Created_At" >`) {
		t.Fatalf("unexpected sql: %s", sql)
	}
	if len(args) != 1 {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestBuildTableWhereClauseUnknownColumn(t *testing.T) {
	cols := []tableQueryColumn{
		{Name: "Name", DataType: "text"},
	}
	quoted := map[string]string{
		"Name": `"Name"`,
	}

	_, _, structured, err := buildTableWhereClause(`Age < 50`, cols, quoted)
	if err == nil {
		t.Fatal("expected error for unknown column")
	}
	if !structured {
		t.Fatal("expected structured mode when parser sees operator syntax")
	}
}

func TestInferReferenceRecordIDKindDefaultsToUUID(t *testing.T) {
	tests := []struct {
		tableName string
		want      string
	}{
		{tableName: "_group", want: ""},
		{tableName: "_group_membership", want: ""},
		{tableName: "_group_role", want: ""},
		{tableName: "_role", want: ""},
		{tableName: "_role_inheritance", want: ""},
		{tableName: "_user", want: ""},
	}

	for _, tt := range tests {
		if got := inferReferenceRecordIDKind(tt.tableName); got != tt.want {
			t.Fatalf("inferReferenceRecordIDKind(%q) = %q, want %q", tt.tableName, got, tt.want)
		}
	}
}
