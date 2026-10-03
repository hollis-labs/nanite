package workflowhost

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

type preflightRecordScanner struct {
	row     workflowScanner
	keys    []int
	names   []string
	id      string
	columns []string
	actual  string
}

func (s *preflightRecordScanner) Scan(destinations ...any) error {
	err := s.row.Scan(destinations...)
	var parts []string
	for index, key := range s.keys {
		if key < len(destinations) {
			parts = append(parts, s.names[index]+"="+fmt.Sprint(reflect.ValueOf(destinations[key]).Elem().Interface()))
		}
	}
	s.id = strings.Join(parts, " ")
	var fields []string
	for i, column := range s.columns {
		if i < len(destinations) && !strings.HasSuffix(column, "_json") {
			fields = append(fields, column+"="+fmt.Sprint(reflect.ValueOf(destinations[i]).Elem().Interface()))
		}
	}
	s.actual = strings.Join(fields, " ")
	return err
}

func preflightEachID(ctx context.Context, tx workflowSQL, validation, table, column, statement string, load func(string) error, args ...any) error {
	s := preflightState(ctx)
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return s.finding("fatal", validation, table, "scan", "readable persisted records", err)
	}
	defer closeRows(rows)
	s.count(validation, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return s.finding("fatal", validation, table, "scan", "readable record id", err)
		}
		s.count(validation, 1)
		if err := load(id); err != nil {
			if err := s.finding("fatal", validation, table, column+"="+id, "valid persisted record", err); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return s.finding("fatal", validation, table, "scan", "complete record scan", err)
	}
	return nil
}

func preflightRelation(ctx context.Context, tx workflowSQL, validation, table, severity, statement, source string) error {
	s := preflightState(ctx)
	rows, err := tx.QueryContext(ctx, statement)
	if err != nil {
		return s.finding("fatal", validation, table, "scan", "readable relationship", err)
	}
	for rows.Next() {
		var id, expected, actual string
		if err := rows.Scan(&id, &expected, &actual); err != nil {
			closeRows(rows)
			return s.finding("fatal", validation, table, "scan", "readable relationship", err)
		}
		if err := s.finding(severity, validation, table, id, expected, actual); err != nil {
			closeRows(rows)
			return err
		}
	}
	err = rows.Err()
	closeRows(rows)
	if err != nil {
		return s.finding("fatal", validation, table, "scan", "complete relationship scan", err)
	}
	var count int64
	if err := tx.QueryRowContext(ctx, source).Scan(&count); err != nil {
		return s.finding("fatal", validation, table, "scan", "counted logical source rows", err)
	}
	s.count(validation, count)
	return nil
}
