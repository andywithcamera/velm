package db

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type expressionValueKind string

const (
	expressionStringKind expressionValueKind = "string"
	expressionNumberKind expressionValueKind = "number"
	expressionBoolKind   expressionValueKind = "bool"
	expressionTimeKind   expressionValueKind = "time"
)

type expressionValue struct {
	kind        expressionValueKind
	stringValue string
	numberValue float64
	boolValue   bool
	timeValue   time.Time
}

type expressionContext struct {
	formData     map[string]string
	currentField string
	currentTime  time.Time
}

type expressionNode interface {
	eval(ctx expressionContext) (expressionValue, error)
}

type expressionLiteralNode struct {
	value expressionValue
}

type expressionIdentifierNode struct {
	name string
}

type expressionUnaryNode struct {
	op   string
	node expressionNode
}

type expressionBinaryNode struct {
	op    string
	left  expressionNode
	right expressionNode
}

type expressionCallNode struct {
	name string
	args []expressionNode
}

type expressionTokenType int

const (
	expressionTokenEOF expressionTokenType = iota
	expressionTokenIdentifier
	expressionTokenString
	expressionTokenNumber
	expressionTokenBool
	expressionTokenEmpty
	expressionTokenOperator
	expressionTokenLParen
	expressionTokenRParen
	expressionTokenComma
)

type expressionToken struct {
	typ   expressionTokenType
	value string
}

type expressionParser struct {
	tokens []expressionToken
	index  int
}

func validateBooleanExpressionSyntax(expr string) error {
	_, err := parseBooleanExpression(expr)
	return err
}

func evaluateBooleanExpression(expr string, formData map[string]string, currentField string) (bool, error) {
	node, err := parseBooleanExpression(expr)
	if err != nil {
		return false, err
	}
	value, err := node.eval(expressionContext{
		formData:     formData,
		currentField: strings.TrimSpace(currentField),
		currentTime:  time.Now(),
	})
	if err != nil {
		return false, err
	}
	return expressionTruthy(value), nil
}

func parseBooleanExpression(expr string) (expressionNode, error) {
	tokens, err := scanExpressionTokens(expr)
	if err != nil {
		return nil, err
	}
	parser := &expressionParser{tokens: tokens}
	node, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if token := parser.peek(); token.typ != expressionTokenEOF {
		return nil, fmt.Errorf("unexpected token %q", token.value)
	}
	return node, nil
}

func (n expressionLiteralNode) eval(_ expressionContext) (expressionValue, error) {
	return n.value, nil
}

func (n expressionIdentifierNode) eval(ctx expressionContext) (expressionValue, error) {
	name := strings.TrimSpace(n.name)
	if strings.EqualFold(name, "value") && ctx.currentField != "" {
		return newExpressionStringValue(ctx.formData[ctx.currentField]), nil
	}
	if value, ok := ctx.formData[name]; ok {
		return newExpressionStringValue(value), nil
	}
	if normalized := normalizeIdentifier(name); normalized != "" {
		if value, ok := ctx.formData[normalized]; ok {
			return newExpressionStringValue(value), nil
		}
	}
	return newExpressionStringValue(name), nil
}

func (n expressionUnaryNode) eval(ctx expressionContext) (expressionValue, error) {
	value, err := n.node.eval(ctx)
	if err != nil {
		return expressionValue{}, err
	}
	switch n.op {
	case "!":
		return expressionValue{kind: expressionBoolKind, boolValue: !expressionTruthy(value)}, nil
	case "-":
		number, ok := coerceExpressionNumber(value)
		if !ok {
			return expressionValue{}, fmt.Errorf("unary - requires a numeric value")
		}
		return expressionValue{kind: expressionNumberKind, numberValue: -number}, nil
	default:
		return expressionValue{}, fmt.Errorf("unsupported unary operator %q", n.op)
	}
}

func (n expressionBinaryNode) eval(ctx expressionContext) (expressionValue, error) {
	switch n.op {
	case "&&":
		left, err := n.left.eval(ctx)
		if err != nil {
			return expressionValue{}, err
		}
		if !expressionTruthy(left) {
			return expressionValue{kind: expressionBoolKind, boolValue: false}, nil
		}
		right, err := n.right.eval(ctx)
		if err != nil {
			return expressionValue{}, err
		}
		return expressionValue{kind: expressionBoolKind, boolValue: expressionTruthy(right)}, nil
	case "||":
		left, err := n.left.eval(ctx)
		if err != nil {
			return expressionValue{}, err
		}
		if expressionTruthy(left) {
			return expressionValue{kind: expressionBoolKind, boolValue: true}, nil
		}
		right, err := n.right.eval(ctx)
		if err != nil {
			return expressionValue{}, err
		}
		return expressionValue{kind: expressionBoolKind, boolValue: expressionTruthy(right)}, nil
	default:
		left, err := n.left.eval(ctx)
		if err != nil {
			return expressionValue{}, err
		}
		right, err := n.right.eval(ctx)
		if err != nil {
			return expressionValue{}, err
		}
		matched, err := compareExpressionValues(left, right, n.op)
		if err != nil {
			return expressionValue{}, err
		}
		return expressionValue{kind: expressionBoolKind, boolValue: matched}, nil
	}
}

func (n expressionCallNode) eval(ctx expressionContext) (expressionValue, error) {
	args := make([]expressionValue, 0, len(n.args))
	for _, arg := range n.args {
		value, err := arg.eval(ctx)
		if err != nil {
			return expressionValue{}, err
		}
		args = append(args, value)
	}

	switch strings.ToLower(strings.TrimSpace(n.name)) {
	case "len":
		if len(args) != 1 {
			return expressionValue{}, fmt.Errorf("len() expects 1 argument")
		}
		return expressionValue{kind: expressionNumberKind, numberValue: float64(len([]rune(args[0].rawString())))}, nil
	case "lower":
		if len(args) != 1 {
			return expressionValue{}, fmt.Errorf("lower() expects 1 argument")
		}
		return newExpressionStringValue(strings.ToLower(args[0].rawString())), nil
	case "upper":
		if len(args) != 1 {
			return expressionValue{}, fmt.Errorf("upper() expects 1 argument")
		}
		return newExpressionStringValue(strings.ToUpper(args[0].rawString())), nil
	case "trim":
		if len(args) != 1 {
			return expressionValue{}, fmt.Errorf("trim() expects 1 argument")
		}
		return newExpressionStringValue(strings.TrimSpace(args[0].rawString())), nil
	case "empty":
		if len(args) != 1 {
			return expressionValue{}, fmt.Errorf("empty() expects 1 argument")
		}
		return expressionValue{kind: expressionBoolKind, boolValue: strings.TrimSpace(args[0].rawString()) == ""}, nil
	case "contains":
		if len(args) != 2 {
			return expressionValue{}, fmt.Errorf("contains() expects 2 arguments")
		}
		return expressionValue{kind: expressionBoolKind, boolValue: strings.Contains(args[0].rawString(), args[1].rawString())}, nil
	case "startswith":
		if len(args) != 2 {
			return expressionValue{}, fmt.Errorf("startswith() expects 2 arguments")
		}
		return expressionValue{kind: expressionBoolKind, boolValue: strings.HasPrefix(args[0].rawString(), args[1].rawString())}, nil
	case "endswith":
		if len(args) != 2 {
			return expressionValue{}, fmt.Errorf("endswith() expects 2 arguments")
		}
		return expressionValue{kind: expressionBoolKind, boolValue: strings.HasSuffix(args[0].rawString(), args[1].rawString())}, nil
	case "now":
		if len(args) != 0 {
			return expressionValue{}, fmt.Errorf("now() expects 0 arguments")
		}
		return expressionValue{kind: expressionTimeKind, timeValue: ctx.currentTime}, nil
	case "today":
		if len(args) != 0 {
			return expressionValue{}, fmt.Errorf("today() expects 0 arguments")
		}
		current := ctx.currentTime
		return expressionValue{
			kind:      expressionTimeKind,
			timeValue: time.Date(current.Year(), current.Month(), current.Day(), 0, 0, 0, 0, current.Location()),
		}, nil
	default:
		return expressionValue{}, fmt.Errorf("unsupported function %q", n.name)
	}
}

func (p *expressionParser) parseOr() (expressionNode, error) {
	node, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.matchOperator("||") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		node = expressionBinaryNode{op: "||", left: node, right: right}
	}
	return node, nil
}

func (p *expressionParser) parseAnd() (expressionNode, error) {
	node, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.matchOperator("&&") {
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		node = expressionBinaryNode{op: "&&", left: node, right: right}
	}
	return node, nil
}

func (p *expressionParser) parseComparison() (expressionNode, error) {
	node, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	if token := p.peek(); token.typ == expressionTokenOperator && isComparisonOperator(token.value) {
		p.consume()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		node = expressionBinaryNode{op: token.value, left: node, right: right}
	}
	return node, nil
}

func (p *expressionParser) parseUnary() (expressionNode, error) {
	if token := p.peek(); token.typ == expressionTokenOperator && (token.value == "!" || token.value == "-") {
		p.consume()
		node, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return expressionUnaryNode{op: token.value, node: node}, nil
	}
	return p.parsePrimary()
}

func (p *expressionParser) parsePrimary() (expressionNode, error) {
	token := p.consume()
	switch token.typ {
	case expressionTokenIdentifier:
		if p.match(expressionTokenLParen) {
			args := []expressionNode{}
			if !p.match(expressionTokenRParen) {
				for {
					arg, err := p.parseOr()
					if err != nil {
						return nil, err
					}
					args = append(args, arg)
					if p.match(expressionTokenRParen) {
						break
					}
					if !p.match(expressionTokenComma) {
						return nil, fmt.Errorf("expected ',' or ')' after function argument")
					}
				}
			}
			return expressionCallNode{name: token.value, args: args}, nil
		}
		return expressionIdentifierNode{name: token.value}, nil
	case expressionTokenString:
		return expressionLiteralNode{value: newExpressionStringValue(token.value)}, nil
	case expressionTokenNumber:
		number, err := strconv.ParseFloat(token.value, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid numeric literal %q", token.value)
		}
		return expressionLiteralNode{value: expressionValue{kind: expressionNumberKind, numberValue: number}}, nil
	case expressionTokenBool:
		return expressionLiteralNode{value: expressionValue{kind: expressionBoolKind, boolValue: strings.EqualFold(token.value, "true")}}, nil
	case expressionTokenEmpty:
		return expressionLiteralNode{value: newExpressionStringValue("")}, nil
	case expressionTokenLParen:
		node, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.match(expressionTokenRParen) {
			return nil, fmt.Errorf("expected ')'")
		}
		return node, nil
	default:
		return nil, fmt.Errorf("unexpected token %q", token.value)
	}
}

func (p *expressionParser) peek() expressionToken {
	if p.index >= len(p.tokens) {
		return expressionToken{typ: expressionTokenEOF}
	}
	return p.tokens[p.index]
}

func (p *expressionParser) consume() expressionToken {
	token := p.peek()
	if p.index < len(p.tokens) {
		p.index++
	}
	return token
}

func (p *expressionParser) match(expected expressionTokenType) bool {
	if p.peek().typ != expected {
		return false
	}
	p.consume()
	return true
}

func (p *expressionParser) matchOperator(expected string) bool {
	token := p.peek()
	if token.typ != expressionTokenOperator || token.value != expected {
		return false
	}
	p.consume()
	return true
}

func scanExpressionTokens(expr string) ([]expressionToken, error) {
	tokens := make([]expressionToken, 0, 16)
	input := strings.TrimSpace(expr)
	for pos := 0; pos < len(input); {
		ch := rune(input[pos])
		if unicode.IsSpace(ch) {
			pos++
			continue
		}

		switch ch {
		case '(':
			tokens = append(tokens, expressionToken{typ: expressionTokenLParen, value: "("})
			pos++
			continue
		case ')':
			tokens = append(tokens, expressionToken{typ: expressionTokenRParen, value: ")"})
			pos++
			continue
		case ',':
			tokens = append(tokens, expressionToken{typ: expressionTokenComma, value: ","})
			pos++
			continue
		case '\'', '"':
			quote := ch
			pos++
			var value strings.Builder
			for pos < len(input) {
				current := rune(input[pos])
				if current == '\\' && pos+1 < len(input) {
					next := rune(input[pos+1])
					switch next {
					case '\\', '\'', '"':
						value.WriteRune(next)
					case 'n':
						value.WriteByte('\n')
					case 't':
						value.WriteByte('\t')
					default:
						value.WriteRune(next)
					}
					pos += 2
					continue
				}
				if current == quote {
					pos++
					tokens = append(tokens, expressionToken{typ: expressionTokenString, value: value.String()})
					goto nextToken
				}
				value.WriteRune(current)
				pos++
			}
			return nil, fmt.Errorf("unterminated string literal")
		}

		if operator, size := scanExpressionOperator(input[pos:]); size > 0 {
			tokens = append(tokens, expressionToken{typ: expressionTokenOperator, value: operator})
			pos += size
			continue
		}

		if unicode.IsDigit(ch) {
			start := pos
			pos++
			for pos < len(input) {
				current := rune(input[pos])
				if unicode.IsDigit(current) || current == '.' {
					pos++
					continue
				}
				break
			}
			tokens = append(tokens, expressionToken{typ: expressionTokenNumber, value: input[start:pos]})
			continue
		}

		if unicode.IsLetter(ch) || ch == '_' {
			start := pos
			pos++
			for pos < len(input) {
				current := rune(input[pos])
				if unicode.IsLetter(current) || unicode.IsDigit(current) || current == '_' || current == '.' {
					pos++
					continue
				}
				break
			}
			value := input[start:pos]
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "true", "false":
				tokens = append(tokens, expressionToken{typ: expressionTokenBool, value: value})
			case "empty":
				tokens = append(tokens, expressionToken{typ: expressionTokenEmpty, value: value})
			default:
				tokens = append(tokens, expressionToken{typ: expressionTokenIdentifier, value: value})
			}
			continue
		}

		return nil, fmt.Errorf("unexpected character %q", string(ch))

	nextToken:
		continue
	}

	tokens = append(tokens, expressionToken{typ: expressionTokenEOF})
	return tokens, nil
}

func scanExpressionOperator(input string) (string, int) {
	switch {
	case strings.HasPrefix(input, "&&"):
		return "&&", 2
	case strings.HasPrefix(input, "||"):
		return "||", 2
	case strings.HasPrefix(input, "!="):
		return "!=", 2
	case strings.HasPrefix(input, "=="):
		return "==", 2
	case strings.HasPrefix(input, "<="):
		return "<=", 2
	case strings.HasPrefix(input, ">="):
		return ">=", 2
	case strings.HasPrefix(input, "="):
		return "=", 1
	case strings.HasPrefix(input, "<"):
		return "<", 1
	case strings.HasPrefix(input, ">"):
		return ">", 1
	case strings.HasPrefix(input, "!"):
		return "!", 1
	case strings.HasPrefix(input, "-"):
		return "-", 1
	default:
		return "", 0
	}
}

func isComparisonOperator(input string) bool {
	switch input {
	case "=", "==", "!=", "<", "<=", ">", ">=":
		return true
	default:
		return false
	}
}

func compareExpressionValues(left, right expressionValue, op string) (bool, error) {
	if leftBool, ok := coerceExpressionBool(left); ok {
		if rightBool, ok := coerceExpressionBool(right); ok {
			switch op {
			case "=", "==":
				return leftBool == rightBool, nil
			case "!=":
				return leftBool != rightBool, nil
			default:
				return false, fmt.Errorf("operator %q does not support boolean comparisons", op)
			}
		}
	}

	if leftNumber, ok := coerceExpressionNumber(left); ok {
		if rightNumber, ok := coerceExpressionNumber(right); ok {
			switch op {
			case "=", "==":
				return leftNumber == rightNumber, nil
			case "!=":
				return leftNumber != rightNumber, nil
			case "<":
				return leftNumber < rightNumber, nil
			case "<=":
				return leftNumber <= rightNumber, nil
			case ">":
				return leftNumber > rightNumber, nil
			case ">=":
				return leftNumber >= rightNumber, nil
			}
		}
	}

	if leftTime, ok := coerceExpressionTime(left); ok {
		if rightTime, ok := coerceExpressionTime(right); ok {
			switch op {
			case "=", "==":
				return leftTime.Equal(rightTime), nil
			case "!=":
				return !leftTime.Equal(rightTime), nil
			case "<":
				return leftTime.Before(rightTime), nil
			case "<=":
				return leftTime.Before(rightTime) || leftTime.Equal(rightTime), nil
			case ">":
				return leftTime.After(rightTime), nil
			case ">=":
				return leftTime.After(rightTime) || leftTime.Equal(rightTime), nil
			}
		}
	}

	leftString := left.rawString()
	rightString := right.rawString()
	switch op {
	case "=", "==":
		return leftString == rightString, nil
	case "!=":
		return leftString != rightString, nil
	case "<":
		return leftString < rightString, nil
	case "<=":
		return leftString <= rightString, nil
	case ">":
		return leftString > rightString, nil
	case ">=":
		return leftString >= rightString, nil
	default:
		return false, fmt.Errorf("unsupported comparison operator %q", op)
	}
}

func expressionTruthy(value expressionValue) bool {
	switch value.kind {
	case expressionBoolKind:
		return value.boolValue
	case expressionNumberKind:
		return value.numberValue != 0
	case expressionTimeKind:
		return !value.timeValue.IsZero()
	default:
		switch strings.ToLower(strings.TrimSpace(value.rawString())) {
		case "", "0", "false", "no", "off":
			return false
		default:
			return true
		}
	}
}

func coerceExpressionBool(value expressionValue) (bool, bool) {
	switch value.kind {
	case expressionBoolKind:
		return value.boolValue, true
	case expressionStringKind:
		switch strings.ToLower(strings.TrimSpace(value.stringValue)) {
		case "true", "1", "yes", "on":
			return true, true
		case "false", "0", "no", "off", "":
			return false, true
		default:
			return false, false
		}
	default:
		return false, false
	}
}

func coerceExpressionNumber(value expressionValue) (float64, bool) {
	switch value.kind {
	case expressionNumberKind:
		return value.numberValue, true
	case expressionStringKind:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value.stringValue), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func coerceExpressionTime(value expressionValue) (time.Time, bool) {
	switch value.kind {
	case expressionTimeKind:
		return value.timeValue, true
	case expressionStringKind:
		trimmed := strings.TrimSpace(value.stringValue)
		if trimmed == "" {
			return time.Time{}, false
		}
		if parsed, err := parseTimestamp(trimmed); err == nil {
			return parsed, true
		}
		if parsed, err := time.Parse("2006-01-02", trimmed); err == nil {
			return parsed, true
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

func newExpressionStringValue(value string) expressionValue {
	return expressionValue{
		kind:        expressionStringKind,
		stringValue: strings.TrimSpace(value),
	}
}

func (v expressionValue) rawString() string {
	switch v.kind {
	case expressionBoolKind:
		if v.boolValue {
			return "true"
		}
		return "false"
	case expressionNumberKind:
		return strconv.FormatFloat(v.numberValue, 'f', -1, 64)
	case expressionTimeKind:
		return v.timeValue.Format(time.RFC3339)
	default:
		return strings.TrimSpace(v.stringValue)
	}
}
