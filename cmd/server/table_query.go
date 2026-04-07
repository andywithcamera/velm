package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"velm/internal/db"
)

type tableQueryColumn struct {
	Name         string
	Label        string
	DataType     string
	IsDateLike   bool
	IsNumber     bool
	IsBoolean    bool
	InputKind    string
	ReferenceTo  string
	ChoiceLabels map[string]string
}

type tableQueryTokenType int

const (
	tableQueryTokenEOF tableQueryTokenType = iota
	tableQueryTokenIdentifier
	tableQueryTokenLiteral
	tableQueryTokenOperator
	tableQueryTokenLParen
	tableQueryTokenRParen
)

type tableQueryToken struct {
	Type  tableQueryTokenType
	Value string
}

type tableQueryCondition struct {
	Column   string
	Operator string
	Value    string
}

type tableQueryNode struct {
	Kind      string
	Condition tableQueryCondition
	Left      *tableQueryNode
	Right     *tableQueryNode
}

type tableQueryParser struct {
	tokens []tableQueryToken
	pos    int
}

func loadTableQueryColumns(tableName string, physicalCols []string) []tableQueryColumn {
	metas := make(map[string]db.Column)
	table := db.GetTable(tableName)
	if table.ID != "" {
		if cols, err := db.GetColumns(table.ID); err == nil {
			for _, col := range cols {
				metas[strings.ToLower(col.NAME)] = col
			}
		}
	}

	result := make([]tableQueryColumn, 0, len(physicalCols))
	for _, name := range physicalCols {
		meta, ok := metas[strings.ToLower(name)]
		label := name
		dataType := "text"
		if ok {
			if strings.TrimSpace(meta.LABEL) != "" {
				label = meta.LABEL
			}
			if strings.TrimSpace(meta.DATA_TYPE) != "" {
				dataType = meta.DATA_TYPE
			}
		}
		referenceTo := ""
		choiceLabels := map[string]string{}
		if ok {
			if meta.REFERENCE_TABLE.Valid {
				referenceTo = strings.TrimSpace(meta.REFERENCE_TABLE.String)
			}
			for _, choice := range meta.CHOICES {
				value := strings.TrimSpace(choice.Value)
				if value == "" {
					continue
				}
				label := strings.TrimSpace(choice.Label)
				if label == "" {
					label = value
				}
				choiceLabels[value] = label
			}
		}
		if referenceTo == "" && strings.HasSuffix(strings.ToLower(name), "_id") {
			referenceTo = inferReferenceTable(tableName, name)
		}
		if len(choiceLabels) == 0 && strings.HasPrefix(strings.ToLower(strings.TrimSpace(dataType)), "enum:") {
			for _, item := range strings.Split(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(dataType)), "enum:"), "|") {
				value := strings.TrimSpace(item)
				if value == "" {
					continue
				}
				choiceLabels[value] = strings.ReplaceAll(value, "_", " ")
			}
		}
		result = append(result, tableQueryColumn{
			Name:         name,
			Label:        label,
			DataType:     dataType,
			IsDateLike:   isDateLikeDataType(dataType, name),
			IsNumber:     isNumericDataType(dataType, name),
			IsBoolean:    isBooleanDataType(dataType, name),
			InputKind:    queryInputKind(dataType, name),
			ReferenceTo:  referenceTo,
			ChoiceLabels: choiceLabels,
		})
	}
	return result
}

func inferReferenceRecordIDKind(tableName string) string {
	return ""
}

func buildTableWhereClause(raw string, columns []tableQueryColumn, quotedCols map[string]string) (string, []any, bool, error) {
	filter := strings.TrimSpace(raw)
	if filter == "" {
		return "", nil, false, nil
	}

	metaByName := make(map[string]tableQueryColumn, len(columns)*2)
	for _, col := range columns {
		metaByName[strings.ToLower(col.Name)] = col
		metaByName[normalizeQueryLookupKey(col.Name)] = col
		metaByName[normalizeQueryLookupKey(col.Label)] = col
	}

	if !looksLikeStructuredQuery(filter, metaByName) {
		return buildPlainTextWhereClause(columns, quotedCols), []any{"%" + filter + "%"}, false, nil
	}

	tokens, err := tokenizeTableQuery(filter)
	if err != nil {
		return "", nil, true, err
	}
	parser := &tableQueryParser{tokens: tokens}
	root, err := parser.parseExpression()
	if err != nil {
		return "", nil, true, err
	}
	if parser.peek().Type != tableQueryTokenEOF {
		return "", nil, true, fmt.Errorf("unexpected token %q", parser.peek().Value)
	}

	args := make([]any, 0, 8)
	sql, err := buildStructuredQuerySQL(root, metaByName, quotedCols, &args)
	if err != nil {
		return "", nil, true, err
	}
	return sql, args, true, nil
}

func buildPlainTextWhereClause(columns []tableQueryColumn, quotedCols map[string]string) string {
	parts := make([]string, 0, len(columns))
	for _, col := range columns {
		quoted := quotedCols[col.Name]
		parts = append(parts, fmt.Sprintf("CAST(%s AS TEXT) ILIKE $1", quoted))
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func looksLikeStructuredQuery(raw string, metaByName map[string]tableQueryColumn) bool {
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "&&") || strings.Contains(lower, "||") {
		return true
	}
	for name := range metaByName {
		if strings.Contains(lower, name+"=") || strings.Contains(lower, name+" ") {
			return true
		}
	}
	return strings.ContainsAny(raw, "<>!=")
}

func tokenizeTableQuery(raw string) ([]tableQueryToken, error) {
	tokens := make([]tableQueryToken, 0, 16)
	for i := 0; i < len(raw); {
		ch := raw[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n':
			i++
		case ch == '(':
			tokens = append(tokens, tableQueryToken{Type: tableQueryTokenLParen, Value: "("})
			i++
		case ch == ')':
			tokens = append(tokens, tableQueryToken{Type: tableQueryTokenRParen, Value: ")"})
			i++
		case i+1 < len(raw) && (raw[i:i+2] == "&&" || raw[i:i+2] == "||" || raw[i:i+2] == ">=" || raw[i:i+2] == "<=" || raw[i:i+2] == "!="):
			tokens = append(tokens, tableQueryToken{Type: tableQueryTokenOperator, Value: raw[i : i+2]})
			i += 2
		case ch == '=' || ch == '>' || ch == '<':
			tokens = append(tokens, tableQueryToken{Type: tableQueryTokenOperator, Value: string(ch)})
			i++
		case ch == '"' || ch == '\'':
			quote := ch
			i++
			start := i
			for i < len(raw) && raw[i] != quote {
				i++
			}
			if i >= len(raw) {
				return nil, fmt.Errorf("unterminated quoted value")
			}
			tokens = append(tokens, tableQueryToken{Type: tableQueryTokenLiteral, Value: raw[start:i]})
			i++
		default:
			start := i
			for i < len(raw) && !strings.ContainsRune(" \t\n()=><!", rune(raw[i])) {
				if i+1 < len(raw) && (raw[i:i+2] == "&&" || raw[i:i+2] == "||") {
					break
				}
				i++
			}
			value := strings.TrimSpace(raw[start:i])
			tokenType := tableQueryTokenLiteral
			if isIdentifierToken(value) {
				tokenType = tableQueryTokenIdentifier
			}
			tokens = append(tokens, tableQueryToken{Type: tokenType, Value: value})
		}
	}
	tokens = append(tokens, tableQueryToken{Type: tableQueryTokenEOF})
	return tokens, nil
}

func isIdentifierToken(value string) bool {
	if value == "" {
		return false
	}
	for i, ch := range value {
		if !(ch == '_' || ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z') {
			return false
		}
		if i == 0 && ch >= '0' && ch <= '9' {
			return false
		}
	}
	return true
}

func (p *tableQueryParser) peek() tableQueryToken {
	if p.pos >= len(p.tokens) {
		return tableQueryToken{Type: tableQueryTokenEOF}
	}
	return p.tokens[p.pos]
}

func (p *tableQueryParser) next() tableQueryToken {
	token := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return token
}

func (p *tableQueryParser) parseExpression() (*tableQueryNode, error) {
	return p.parseOr()
}

func (p *tableQueryParser) parseOr() (*tableQueryNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == tableQueryTokenOperator && p.peek().Value == "||" {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &tableQueryNode{Kind: "or", Left: left, Right: right}
	}
	return left, nil
}

func (p *tableQueryParser) parseAnd() (*tableQueryNode, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == tableQueryTokenOperator && p.peek().Value == "&&" {
		p.next()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = &tableQueryNode{Kind: "and", Left: left, Right: right}
	}
	return left, nil
}

func (p *tableQueryParser) parsePrimary() (*tableQueryNode, error) {
	if p.peek().Type == tableQueryTokenLParen {
		p.next()
		node, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if p.peek().Type != tableQueryTokenRParen {
			return nil, fmt.Errorf("expected )")
		}
		p.next()
		return node, nil
	}
	return p.parseCondition()
}

func (p *tableQueryParser) parseCondition() (*tableQueryNode, error) {
	columnParts := make([]string, 0, 2)
	for {
		token := p.peek()
		if token.Type != tableQueryTokenIdentifier && token.Type != tableQueryTokenLiteral {
			break
		}
		nextToken := p.tokens[p.pos+1]
		if nextToken.Type == tableQueryTokenOperator && isComparisonOperator(nextToken.Value) {
			columnParts = append(columnParts, token.Value)
			p.next()
			break
		}
		columnParts = append(columnParts, token.Value)
		p.next()
	}
	if len(columnParts) == 0 {
		return nil, fmt.Errorf("expected column name")
	}

	column := strings.TrimSpace(strings.Join(columnParts, " "))
	operator := p.next()
	if operator.Type != tableQueryTokenOperator || !isComparisonOperator(operator.Value) {
		return nil, fmt.Errorf("expected operator after %s", column)
	}

	valueParts := make([]string, 0, 2)
	for {
		token := p.peek()
		if token.Type == tableQueryTokenEOF || token.Type == tableQueryTokenRParen {
			break
		}
		if token.Type == tableQueryTokenOperator && isLogicalOperator(token.Value) {
			break
		}
		if token.Type != tableQueryTokenLiteral && token.Type != tableQueryTokenIdentifier {
			return nil, fmt.Errorf("expected value after %s", operator.Value)
		}
		valueParts = append(valueParts, token.Value)
		p.next()
	}
	if len(valueParts) == 0 {
		return nil, fmt.Errorf("expected value after %s", operator.Value)
	}
	value := strings.TrimSpace(strings.Join(valueParts, " "))
	return &tableQueryNode{
		Kind: "condition",
		Condition: tableQueryCondition{
			Column:   column,
			Operator: operator.Value,
			Value:    value,
		},
	}, nil
}

func buildStructuredQuerySQL(node *tableQueryNode, metaByName map[string]tableQueryColumn, quotedCols map[string]string, args *[]any) (string, error) {
	if node == nil {
		return "", fmt.Errorf("empty query")
	}
	switch node.Kind {
	case "and":
		left, err := buildStructuredQuerySQL(node.Left, metaByName, quotedCols, args)
		if err != nil {
			return "", err
		}
		right, err := buildStructuredQuerySQL(node.Right, metaByName, quotedCols, args)
		if err != nil {
			return "", err
		}
		return "(" + left + " AND " + right + ")", nil
	case "or":
		left, err := buildStructuredQuerySQL(node.Left, metaByName, quotedCols, args)
		if err != nil {
			return "", err
		}
		right, err := buildStructuredQuerySQL(node.Right, metaByName, quotedCols, args)
		if err != nil {
			return "", err
		}
		return "(" + left + " OR " + right + ")", nil
	case "condition":
		return buildConditionSQL(node.Condition, metaByName, quotedCols, args)
	default:
		return "", fmt.Errorf("unknown query node")
	}
}

func buildConditionSQL(cond tableQueryCondition, metaByName map[string]tableQueryColumn, quotedCols map[string]string, args *[]any) (string, error) {
	columnKey := normalizeQueryLookupKey(cond.Column)
	meta, ok := metaByName[columnKey]
	if !ok {
		return "", fmt.Errorf("unknown column %q", cond.Column)
	}
	quoted := quotedCols[meta.Name]
	if quoted == "" {
		return "", fmt.Errorf("column %q is not queryable", cond.Column)
	}

	value := strings.TrimSpace(cond.Value)
	switch {
	case meta.IsDateLike:
		parsed, err := parseDateLikeValue(value)
		if err != nil {
			return "", fmt.Errorf("invalid date/time for %s", meta.Name)
		}
		*args = append(*args, parsed)
		return fmt.Sprintf("%s %s $%d", quoted, normalizeOperator(cond.Operator), len(*args)), nil
	case meta.IsNumber:
		num, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return "", fmt.Errorf("invalid number for %s", meta.Name)
		}
		*args = append(*args, num)
		return fmt.Sprintf("%s %s $%d", quoted, normalizeOperator(cond.Operator), len(*args)), nil
	case meta.IsBoolean:
		boolVal, err := parseBoolValue(value)
		if err != nil {
			return "", fmt.Errorf("invalid boolean for %s", meta.Name)
		}
		*args = append(*args, boolVal)
		return fmt.Sprintf("%s %s $%d", quoted, normalizeOperator(cond.Operator), len(*args)), nil
	default:
		return buildTextConditionSQL(quoted, cond.Operator, value, args), nil
	}
}

func buildTextConditionSQL(quotedCol, operator, value string, args *[]any) string {
	if operator == "=" || operator == "!=" {
		if strings.Contains(value, "*") {
			*args = append(*args, strings.ReplaceAll(value, "*", "%"))
			op := "ILIKE"
			if operator == "!=" {
				op = "NOT ILIKE"
			}
			return fmt.Sprintf("CAST(%s AS TEXT) %s $%d", quotedCol, op, len(*args))
		}
		*args = append(*args, value)
		op := "="
		if operator == "!=" {
			op = "<>"
		}
		return fmt.Sprintf("LOWER(CAST(%s AS TEXT)) %s LOWER($%d)", quotedCol, op, len(*args))
	}
	*args = append(*args, value)
	return fmt.Sprintf("LOWER(CAST(%s AS TEXT)) %s LOWER($%d)", quotedCol, normalizeOperator(operator), len(*args))
}

func normalizeOperator(operator string) string {
	switch operator {
	case "=", "!=", "<", ">", "<=", ">=":
		if operator == "!=" {
			return "<>"
		}
		return operator
	default:
		return "="
	}
}

func isDateLikeDataType(dataType, name string) bool {
	lower := strings.ToLower(strings.TrimSpace(dataType))
	return strings.Contains(lower, "date") || strings.Contains(lower, "time") || strings.Contains(lower, "timestamp") || strings.HasSuffix(strings.ToLower(name), "_at")
}

func isNumericDataType(dataType, name string) bool {
	lower := strings.ToLower(strings.TrimSpace(dataType))
	return strings.Contains(lower, "int") || strings.Contains(lower, "numeric") || strings.Contains(lower, "decimal") || strings.Contains(lower, "float") || strings.Contains(lower, "double") || strings.Contains(lower, "serial")
}

func isBooleanDataType(dataType, name string) bool {
	lower := strings.ToLower(strings.TrimSpace(dataType))
	return lower == "bool" || lower == "boolean" || strings.HasPrefix(strings.ToLower(name), "is_")
}

func queryInputKind(dataType, name string) string {
	switch {
	case isDateLikeDataType(dataType, name):
		if strings.Contains(strings.ToLower(dataType), "time") {
			return "datetime-local"
		}
		return "date"
	case isNumericDataType(dataType, name):
		return "number"
	case isBooleanDataType(dataType, name):
		return "boolean"
	default:
		return "text"
	}
}

func parseDateLikeValue(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date")
}

func parseBoolValue(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid bool")
	}
}

func normalizeQueryLookupKey(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, lower)
}

func isComparisonOperator(value string) bool {
	switch value {
	case "=", "!=", "<", ">", "<=", ">=":
		return true
	default:
		return false
	}
}

func isLogicalOperator(value string) bool {
	return value == "&&" || value == "||"
}
