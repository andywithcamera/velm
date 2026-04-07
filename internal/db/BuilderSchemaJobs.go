package db

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type BuilderSchemaJob struct {
	ID        int64
	TableName string
	DryRun    bool
	Status    string
	CreatedBy string
	CreatedAt time.Time
	Executed  *time.Time
	ErrorText string
}

type BuilderSchemaJobStep struct {
	ID          int64
	JobID       int64
	Sequence    int
	Action      string
	Statement   string
	RollbackSQL string
	Status      string
	ErrorText   string
}

type schemaPlanStep struct {
	Sequence    int
	Action      string
	Statement   string
	RollbackSQL string
}

func PlanAndApplyBuilderSchema(ctx context.Context, tableName string, dryRun bool, userID string) (int64, error) {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if !IsSafeIdentifier(tableName) {
		return 0, fmt.Errorf("invalid table name")
	}
	if isProtectedTableName(tableName) {
		return 0, fmt.Errorf("table %q is protected", tableName)
	}

	steps, err := buildSchemaPlanSteps(ctx, tableName)
	if err != nil {
		return 0, err
	}

	jobID, err := createBuilderSchemaJob(ctx, tableName, dryRun, userID, steps)
	if err != nil {
		return 0, err
	}

	if dryRun || len(steps) == 0 {
		if !dryRun {
			if _, err := Pool.Exec(ctx, `UPDATE _builder_schema_job SET status = 'applied', executed_at = NOW() WHERE _id = $1`, jobID); err != nil {
				return 0, fmt.Errorf("mark empty apply job complete: %w", err)
			}
		}
		return jobID, nil
	}

	if err := executeBuilderSchemaJob(ctx, jobID); err != nil {
		return jobID, err
	}

	return jobID, nil
}

func GetBuilderSchemaJob(ctx context.Context, jobID int64) (BuilderSchemaJob, error) {
	var job BuilderSchemaJob
	var executedAt *time.Time
	err := Pool.QueryRow(ctx, `
		SELECT _id, table_name, dry_run, status, COALESCE(created_by, ''), created_at, executed_at, COALESCE(error_text, '')
		FROM _builder_schema_job
		WHERE _id = $1
	`, jobID).Scan(&job.ID, &job.TableName, &job.DryRun, &job.Status, &job.CreatedBy, &job.CreatedAt, &executedAt, &job.ErrorText)
	if err != nil {
		return BuilderSchemaJob{}, fmt.Errorf("schema job not found")
	}
	job.Executed = executedAt
	return job, nil
}

func ListBuilderSchemaJobSteps(ctx context.Context, jobID int64) ([]BuilderSchemaJobStep, error) {
	rows, err := Pool.Query(ctx, `
		SELECT _id, job_id, seq, action, statement_sql, COALESCE(rollback_sql, ''), status, COALESCE(error_text, '')
		FROM _builder_schema_job_step
		WHERE job_id = $1
		ORDER BY seq ASC
	`, jobID)
	if err != nil {
		return nil, fmt.Errorf("list schema job steps: %w", err)
	}
	defer rows.Close()

	steps := make([]BuilderSchemaJobStep, 0, 16)
	for rows.Next() {
		var step BuilderSchemaJobStep
		if err := rows.Scan(&step.ID, &step.JobID, &step.Sequence, &step.Action, &step.Statement, &step.RollbackSQL, &step.Status, &step.ErrorText); err != nil {
			return nil, fmt.Errorf("scan schema job step: %w", err)
		}
		steps = append(steps, step)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema job steps: %w", err)
	}
	return steps, nil
}

func createBuilderSchemaJob(ctx context.Context, tableName string, dryRun bool, userID string, steps []schemaPlanStep) (int64, error) {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin schema job create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var jobID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO _builder_schema_job (table_name, dry_run, status, created_by)
		VALUES ($1, $2, 'planned', NULLIF($3, ''))
		RETURNING _id
	`, tableName, dryRun, strings.TrimSpace(userID)).Scan(&jobID); err != nil {
		return 0, fmt.Errorf("insert schema job: %w", err)
	}

	for _, step := range steps {
		if _, err := tx.Exec(ctx, `
			INSERT INTO _builder_schema_job_step (job_id, seq, action, statement_sql, rollback_sql, status)
			VALUES ($1, $2, $3, $4, NULLIF($5, ''), 'planned')
		`, jobID, step.Sequence, step.Action, step.Statement, step.RollbackSQL); err != nil {
			return 0, fmt.Errorf("insert schema job step: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit schema job create: %w", err)
	}
	return jobID, nil
}

func executeBuilderSchemaJob(ctx context.Context, jobID int64) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin schema job execution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT _id, action, statement_sql
		FROM _builder_schema_job_step
		WHERE job_id = $1
		ORDER BY seq ASC
	`, jobID)
	if err != nil {
		return fmt.Errorf("load job steps: %w", err)
	}
	defer rows.Close()

	type execStep struct {
		ID        int64
		Action    string
		Statement string
	}
	steps := make([]execStep, 0, 16)
	for rows.Next() {
		var s execStep
		if err := rows.Scan(&s.ID, &s.Action, &s.Statement); err != nil {
			return fmt.Errorf("scan execution step: %w", err)
		}
		steps = append(steps, s)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate execution steps: %w", err)
	}

	for _, step := range steps {
		if strings.HasPrefix(step.Action, "warn_") {
			if _, err := tx.Exec(ctx, `UPDATE _builder_schema_job_step SET status = 'skipped' WHERE _id = $1`, step.ID); err != nil {
				return fmt.Errorf("mark warning step skipped: %w", err)
			}
			continue
		}
		if _, err := tx.Exec(ctx, step.Statement); err != nil {
			_, _ = Pool.Exec(ctx, `
				UPDATE _builder_schema_job
				SET status = 'failed', executed_at = NOW(), error_text = $2
				WHERE _id = $1
			`, jobID, err.Error())
			_, _ = Pool.Exec(ctx, `
				UPDATE _builder_schema_job_step
				SET status = CASE WHEN _id = $2 THEN 'failed' ELSE status END,
					error_text = CASE WHEN _id = $2 THEN $3 ELSE error_text END
				WHERE job_id = $1
			`, jobID, step.ID, err.Error())
			return fmt.Errorf("execute schema step failed: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE _builder_schema_job_step SET status = 'applied' WHERE _id = $1`, step.ID); err != nil {
			return fmt.Errorf("mark step applied: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE _builder_schema_job
		SET status = 'applied', executed_at = NOW(), error_text = NULL
		WHERE _id = $1
	`, jobID); err != nil {
		return fmt.Errorf("mark job applied: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit schema job execution: %w", err)
	}
	return nil
}

func buildSchemaPlanSteps(ctx context.Context, tableName string) ([]schemaPlanStep, error) {
	if _, err := GetBuilderTable(ctx, tableName); err != nil {
		return nil, err
	}

	quotedTable, err := QuoteIdentifier(tableName)
	if err != nil {
		return nil, fmt.Errorf("invalid table name")
	}

	physicalExists := false
	if err := Pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, tableName).Scan(&physicalExists); err != nil {
		return nil, fmt.Errorf("check physical table: %w", err)
	}

	steps := make([]schemaPlanStep, 0, 32)
	seq := 10
	if !physicalExists {
		steps = append(steps, schemaPlanStep{
			Sequence:    seq,
			Action:      "create_table",
			Statement:   fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (_id UUID PRIMARY KEY DEFAULT gen_random_uuid(), _created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), _updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), _update_count BIGINT NOT NULL DEFAULT 0, _deleted_at TIMESTAMPTZ, _created_by UUID, _updated_by UUID, _deleted_by UUID)`, quotedTable),
			RollbackSQL: fmt.Sprintf(`DROP TABLE IF EXISTS %s`, quotedTable),
		})
		seq += 10
		steps = append(steps, schemaPlanStep{
			Sequence:    seq,
			Action:      "ensure_record_version_trigger",
			Statement:   fmt.Sprintf(`SELECT _ensure_record_version_trigger('%s')`, tableName),
			RollbackSQL: fmt.Sprintf(`DROP TRIGGER IF EXISTS trg_touch_record_version ON %s`, quotedTable),
		})
		seq += 10
	}

	columns, err := ListBuilderColumns(ctx, tableName)
	if err != nil {
		return nil, err
	}
	physicalColumns, err := loadPhysicalColumns(ctx, tableName)
	if err != nil {
		return nil, err
	}
	seenPhysical := map[string]bool{}

	for _, col := range columns {
		colName := strings.ToLower(strings.TrimSpace(col.Name))
		sqlType, err := builderDataTypeToSQL(normalizeDataType(col.DataType))
		if err != nil {
			return nil, err
		}
		quotedCol, err := QuoteIdentifier(colName)
		if err != nil {
			return nil, fmt.Errorf("invalid column name")
		}

		phys, exists := physicalColumns[colName]
		if !exists {
			nullClause := ""
			if !col.IsNullable {
				nullClause = " NOT NULL"
			}
			steps = append(steps, schemaPlanStep{
				Sequence:    seq,
				Action:      "add_column",
				Statement:   fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s%s`, quotedTable, quotedCol, sqlType, nullClause),
				RollbackSQL: fmt.Sprintf(`ALTER TABLE %s DROP COLUMN IF EXISTS %s`, quotedTable, quotedCol),
			})
			seq += 10
			continue
		}
		seenPhysical[colName] = true

		expected := normalizeSQLType(sqlType)
		current := normalizeSQLType(phys.SQLType)
		if expected != current {
			steps = append(steps, schemaPlanStep{
				Sequence: seq,
				Action:   "warn_type_drift",
				Statement: fmt.Sprintf(
					"-- type drift on %s.%s: metadata=%s, physical=%s. Manual review required before applying type change.",
					tableName, colName, expected, current,
				),
				RollbackSQL: "",
			})
			seq += 10
		}

		if col.IsNullable != phys.IsNullable {
			if col.IsNullable {
				steps = append(steps, schemaPlanStep{
					Sequence:    seq,
					Action:      "drop_not_null",
					Statement:   fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL`, quotedTable, quotedCol),
					RollbackSQL: fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s SET NOT NULL`, quotedTable, quotedCol),
				})
				seq += 10
			} else {
				steps = append(steps, schemaPlanStep{
					Sequence: seq,
					Action:   "precheck_no_nulls",
					Statement: fmt.Sprintf(
						`DO $$ BEGIN IF EXISTS (SELECT 1 FROM %s WHERE %s IS NULL) THEN RAISE EXCEPTION 'column %s contains NULL values'; END IF; END $$`,
						quotedTable, quotedCol, colName,
					),
					RollbackSQL: "",
				})
				seq += 10
				steps = append(steps, schemaPlanStep{
					Sequence:    seq,
					Action:      "set_not_null",
					Statement:   fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s SET NOT NULL`, quotedTable, quotedCol),
					RollbackSQL: fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL`, quotedTable, quotedCol),
				})
				seq += 10
			}
		}
	}

	for name, phys := range physicalColumns {
		if seenPhysical[name] || strings.HasPrefix(name, "_") {
			continue
		}
		steps = append(steps, schemaPlanStep{
			Sequence: seq,
			Action:   "warn_extra_column",
			Statement: fmt.Sprintf(
				"-- physical column %s.%s (%s) is not present in metadata. Manual review for drop/rename/backfill.",
				tableName, name, normalizeSQLType(phys.SQLType),
			),
			RollbackSQL: "",
		})
		seq += 10
	}

	sort.Slice(steps, func(i, j int) bool {
		return steps[i].Sequence < steps[j].Sequence
	})
	return steps, nil
}

type physicalColumn struct {
	SQLType    string
	IsNullable bool
}

func loadPhysicalColumns(ctx context.Context, tableName string) (map[string]physicalColumn, error) {
	rows, err := Pool.Query(ctx, `
		SELECT column_name, data_type, udt_name, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = $1
	`, tableName)
	if err != nil {
		return nil, fmt.Errorf("load physical columns: %w", err)
	}
	defer rows.Close()

	columns := map[string]physicalColumn{}
	for rows.Next() {
		var name, dataType, udtName, isNullable string
		if err := rows.Scan(&name, &dataType, &udtName, &isNullable); err != nil {
			return nil, fmt.Errorf("scan physical column: %w", err)
		}
		sqlType := strings.ToLower(strings.TrimSpace(dataType))
		if sqlType == "user-defined" {
			sqlType = strings.ToLower(strings.TrimSpace(udtName))
		}
		columns[strings.ToLower(strings.TrimSpace(name))] = physicalColumn{
			SQLType:    sqlType,
			IsNullable: strings.EqualFold(strings.TrimSpace(isNullable), "yes"),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate physical columns: %w", err)
	}
	return columns, nil
}

func normalizeSQLType(input string) string {
	v := strings.ToLower(strings.TrimSpace(input))
	if strings.HasPrefix(v, "varchar(") {
		return "varchar"
	}
	switch v {
	case "integer":
		return "int"
	case "double precision":
		return "double"
	case "boolean":
		return "bool"
	case "character varying", "varchar":
		return "varchar"
	case "numeric":
		return "numeric"
	case "json", "jsonb":
		return "jsonb"
	case "uuid":
		return "uuid"
	case "text":
		return "text"
	case "date":
		return "date"
	case "timestamp with time zone", "timestamptz":
		return "timestamptz"
	case "timestamp without time zone", "timestamp":
		return "timestamp"
	}
	return v
}
