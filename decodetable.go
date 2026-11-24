// decodetable.go reads the delimited formats, csv and tsv, into records.

package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

// byteOrderMark opens a UTF-8 file that a spreadsheet exported.
const byteOrderMark = "﻿"

// decodeCSV reads RFC 4180 text. The first row names the columns.
func decodeCSV(s string) (any, error) {
	rows, err := csvReader(s).ReadAll()
	if err != nil {
		return nil, err
	}
	return tableRecords(rows), nil
}

func csvReader(s string) *csv.Reader {
	return csv.NewReader(strings.NewReader(strings.TrimPrefix(s, byteOrderMark)))
}

// decodeTSV reads tab-separated text. The format has no quoting, so a quote is data.
func decodeTSV(s string) (any, error) {
	var rows [][]string
	for i, line := range splitLines(strings.TrimPrefix(s, byteOrderMark)) {
		if line == "" {
			continue
		}
		row := strings.Split(line, "\t")
		if len(rows) > 0 && len(row) != len(rows[0]) {
			return nil, fmt.Errorf("line %d has %d fields, and the first row has %d", i+1, len(row), len(rows[0]))
		}
		rows = append(rows, row)
	}
	return tableRecords(rows), nil
}

// tableRecords turns rows into records keyed by the first row. Every cell is a
// string: the format carries no types, and a guess turns a zip code into a number.
func tableRecords(rows [][]string) []any {
	out := []any{}
	if len(rows) == 0 {
		return out
	}
	cols := columnNames(rows[0])
	for _, row := range rows[1:] {
		rec := make(map[string]any, len(cols))
		for i, col := range cols {
			rec[col] = row[i]
		}
		out = append(out, rec)
	}
	return out
}

// columnNames makes a usable key out of each header cell. An empty cell takes
// its position, and a repeated name takes a suffix, so no column is lost.
func columnNames(header []string) []string {
	seen := map[string]int{}
	out := make([]string, len(header))
	for i, name := range header {
		name = strings.TrimSpace(name)
		if name == "" {
			name = fmt.Sprintf("column_%d", i+1)
		}
		seen[name]++
		if n := seen[name]; n > 1 {
			name = fmt.Sprintf("%s_%d", name, n)
		}
		out[i] = name
	}
	return out
}

// tableColumns is the header of a csv or tsv body, in the order it was
// written. A record is a map, which forgets that order.
func tableColumns(b runBody) []string {
	var header []string
	switch b.format {
	case "csv":
		row, err := csvReader(b.text).Read()
		if err != nil && !errors.Is(err, io.EOF) {
			return nil
		}
		header = row
	case "tsv":
		for _, line := range splitLines(strings.TrimPrefix(b.text, byteOrderMark)) {
			if line != "" {
				header = strings.Split(line, "\t")
				break
			}
		}
	}
	if len(header) == 0 {
		return nil
	}
	return columnNames(header)
}

// orderDerived gives a block that declares no fields the columns of a csv or
// tsv body in header order. Without it the formatter derives them sorted.
func orderDerived(blocks []FieldsBlock, b runBody) []FieldsBlock {
	cols := tableColumns(b)
	if len(cols) == 0 {
		return blocks
	}
	out := make([]FieldsBlock, len(blocks))
	for i, blk := range blocks {
		out[i] = blk
		if blk.Fields == nil || len(blk.Fields.List) > 0 || blk.Fields.Over != "" {
			continue
		}
		derived := *blk.Fields
		for _, col := range cols {
			fld := Field{Name: col, Path: col}
			// A dot in a path is a step, so a dotted column reads by index instead.
			if strings.Contains(col, ".") {
				fld = Field{Name: col, Expr: fmt.Sprintf("{{ index . %q }}", col)}
			}
			derived.List = append(derived.List, fld)
		}
		out[i].Fields = &derived
	}
	return out
}
