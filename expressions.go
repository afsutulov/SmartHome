package main

import (
	"errors"
	"strconv"
	"strings"
)

func looksLikeExpression(e string) bool {
	return strings.ContainsAny(e, "()$") || strings.Contains(e, "payload")
}

// Один разбор синтаксиса для проверки конфига и вычисления выражений.
func parseExpressionCall(e string) (string, []string, error) {
	open := strings.IndexByte(e, '(')
	if open <= 0 || !strings.HasSuffix(e, ")") {
		return "", nil, errors.New("expected function(arguments)")
	}
	name := e[:open]
	inside := e[open+1 : len(e)-1]
	var args []string
	depth, start := 0, 0
	var quote byte
	escaped := false
	for i := 0; i < len(inside); i++ {
		c := inside[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return "", nil, errors.New("unexpected closing parenthesis")
			}
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(inside[start:i]))
				start = i + 1
			}
		}
	}
	if quote != 0 || depth != 0 {
		return "", nil, errors.New("unclosed quote or parenthesis")
	}
	args = append(args, strings.TrimSpace(inside[start:]))
	return name, args, nil
}

func expressionLiteral(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("comparison value is empty")
	}
	if value[0] == '"' {
		text, err := strconv.Unquote(value)
		return text, err
	}
	if value[0] == '\'' {
		if len(value) < 2 || value[len(value)-1] != '\'' || strings.ContainsAny(value[1:len(value)-1], "'\\") {
			return "", errors.New("invalid single-quoted comparison value")
		}
		return value[1 : len(value)-1], nil
	}
	if strings.ContainsAny(value, "(),\"' \\$") {
		return "", errors.New("invalid comparison value")
	}
	return value, nil
}

func expressionFields(expr string) []string {
	if strings.HasPrefix(expr, "payload.") {
		return []string{strings.TrimPrefix(expr, "payload.")}
	}
	name, args, err := parseExpressionCall(expr)
	if err != nil {
		return nil
	}
	if name == "and" {
		var fields []string
		for _, arg := range args {
			fields = append(fields, expressionFields(arg)...)
		}
		return fields
	}
	if len(args) > 0 && strings.HasPrefix(args[0], "payload.") {
		return []string{strings.TrimPrefix(args[0], "payload.")}
	}
	return nil
}

func expressionHasIncomingField(expr string, payload map[string]any) bool {
	fields := expressionFields(expr)
	if len(fields) == 0 {
		return true
	}
	for _, field := range fields {
		if _, ok := payload[field]; ok {
			return true
		}
	}
	return false
}

func relevantUpdates(updates map[string]string, payload map[string]any) map[string]string {
	result := map[string]string{}
	for field, expr := range updates {
		if expressionHasIncomingField(expr, payload) {
			result[field] = expr
		}
	}
	return result
}

func reportHasIncomingField(r ReportConfig, payload map[string]any) bool {
	if r.Expression != "" {
		return expressionHasIncomingField(r.Expression, payload)
	}
	_, ok := payload[r.Field]
	return ok
}
