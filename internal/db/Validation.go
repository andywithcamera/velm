package db

import (
	"database/sql"
	"fmt"
	"net/mail"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"velm/internal/utils"
)

var varcharRegex = regexp.MustCompile(`(?i)^varchar\((\d+)\)$`)

func ValidateFormWrite(columns []Column, formData map[string]string, isCreate bool) error {
	for _, col := range columns {
		if strings.HasPrefix(col.NAME, "_") {
			continue
		}
		if !conditionMatches(col.CONDITION_EXPR, formData) {
			continue
		}
		value, hasValue := formData[col.NAME]
		trimmed := strings.TrimSpace(value)

		autoNumberCreate := isCreate && IsAutoNumberDataType(col.DATA_TYPE)
		if !col.IS_NULLABLE && !col.DEFAULT_VALUE.Valid && !autoNumberCreate {
			if isCreate && trimmed == "" {
				return fmt.Errorf("field %q is required", col.NAME)
			}
			if hasValue && trimmed == "" {
				return fmt.Errorf("field %q is required", col.NAME)
			}
		}

		if !hasValue || trimmed == "" {
			if err := validateExpressionRule(col, formData); err != nil {
				return err
			}
			continue
		}

		if err := validateColumnValue(col, trimmed); err != nil {
			return err
		}
		if err := validateRegexRule(col.NAME, col.VALIDATION_REGEX.String, col.VALIDATION_REGEX.Valid, trimmed); err != nil {
			return err
		}
		if err := validateExpressionRule(col, formData); err != nil {
			return err
		}
	}
	return nil
}

func conditionMatches(rule sql.NullString, formData map[string]string) bool {
	if !rule.Valid {
		return true
	}
	expr := strings.TrimSpace(rule.String)
	if expr == "" {
		return true
	}
	matched, err := evaluateBooleanExpression(expr, formData, "")
	if err != nil {
		// Fail open for compatibility with any previously stored ad hoc condition strings.
		return true
	}
	return matched
}

func validateColumnValue(column Column, value string) error {
	return validateDataType(column.NAME, column.DATA_TYPE, value, column.CHOICES, column.PREFIX.String)
}

func validateDataType(columnName, dataType, value string, choices []ChoiceOption, prefix string) error {
	dt := normalizeDataType(dataType)
	baseType := BaseDataType(dt)

	if maxLength := parseMaxLength(dt); maxLength > 0 && len(value) > maxLength {
		return fmt.Errorf("field %q exceeds max length (%d)", columnName, maxLength)
	}

	switch baseType {
	case "int", "integer":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("field %q must be an integer", columnName)
		}
	case "bigint", "bigserial", "serial":
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return fmt.Errorf("field %q must be an integer", columnName)
		}
	case "float", "double", "decimal", "numeric":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Errorf("field %q must be a number", columnName)
		}
	case "bool", "boolean":
		lower := strings.ToLower(value)
		if lower != "true" && lower != "false" {
			return fmt.Errorf("field %q must be true or false", columnName)
		}
	case "uuid", "reference":
		if !utils.IsValidUUID(value) {
			return fmt.Errorf("field %q must be a valid UUID", columnName)
		}
	case "choice":
		if err := validateChoices(columnName, choices, value); err != nil {
			return err
		}
	case "date":
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return fmt.Errorf("field %q must be a valid date", columnName)
		}
	case "timestamp", "timestamptz", "datetime":
		if _, err := parseTimestamp(value); err != nil {
			return fmt.Errorf("field %q must be a valid timestamp", columnName)
		}
	case "email":
		if _, err := mail.ParseAddress(value); err != nil {
			return fmt.Errorf("field %q must be a valid email", columnName)
		}
	case "url":
		parsed, err := neturl.ParseRequestURI(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("field %q must be a valid URL", columnName)
		}
	case "phone":
		if !looksLikePhoneNumber(value) {
			return fmt.Errorf("field %q must be a valid phone number", columnName)
		}
	case "autnumber":
		if err := validateAutoNumberValue(columnName, prefix, value); err != nil {
			return err
		}
	default:
		if strings.HasPrefix(dt, "enum:") {
			if err := validateEnum(columnName, strings.TrimPrefix(dt, "enum:"), value); err != nil {
				return err
			}
		}
	}

	return nil
}

func validateAutoNumberValue(columnName, prefix, value string) error {
	prefix = normalizeAutoNumberPrefix(prefix)
	if err := validateAutoNumberPrefix(prefix); err != nil {
		return fmt.Errorf("field %q has invalid autnumber prefix", columnName)
	}
	pattern := regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + `-[0-9]{6,}$`)
	if !pattern.MatchString(strings.TrimSpace(value)) {
		return fmt.Errorf("field %q must match %s-000001", columnName, prefix)
	}
	return nil
}

func validateExpressionRule(column Column, formData map[string]string) error {
	if !column.VALIDATION_EXPR.Valid {
		return nil
	}
	expr := strings.TrimSpace(column.VALIDATION_EXPR.String)
	if expr == "" {
		return nil
	}
	matched, err := evaluateBooleanExpression(expr, formData, column.NAME)
	if err != nil {
		return fmt.Errorf("field %q has invalid validation expression", column.NAME)
	}
	if matched {
		return nil
	}
	message := strings.TrimSpace(column.VALIDATION_MSG.String)
	if message != "" {
		return fmt.Errorf("field %q: %s", column.NAME, message)
	}
	return fmt.Errorf("field %q failed validation", column.NAME)
}

func validateRegexRule(columnName, rule string, hasRule bool, value string) error {
	if !hasRule {
		return nil
	}
	pattern := strings.TrimSpace(rule)
	if pattern == "" {
		return nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("field %q has invalid validation regex", columnName)
	}
	if !re.MatchString(value) {
		return fmt.Errorf("field %q failed validation", columnName)
	}
	return nil
}

func parseMaxLength(dataType string) int {
	match := varcharRegex.FindStringSubmatch(dataType)
	if len(match) != 2 {
		return 0
	}
	v, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return v
}

func parseTimestamp(raw string) (time.Time, error) {
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp")
}

func validateEnum(columnName, enumList, value string) error {
	values := strings.Split(enumList, "|")
	for _, item := range values {
		if strings.TrimSpace(item) == value {
			return nil
		}
	}
	return fmt.Errorf("field %q must be one of the allowed values", columnName)
}

func validateChoices(columnName string, choices []ChoiceOption, value string) error {
	for _, choice := range choices {
		if strings.TrimSpace(choice.Value) == value {
			return nil
		}
	}
	return fmt.Errorf("field %q must be one of the allowed values", columnName)
}

func looksLikePhoneNumber(value string) bool {
	digits := 0
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '+' || r == '-' || r == ' ' || r == '(' || r == ')' || r == '.':
			continue
		default:
			return false
		}
	}
	return digits >= 6
}
