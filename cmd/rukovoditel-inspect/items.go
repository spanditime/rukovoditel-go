package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spanditime/rukovoditel-go/api"
	"github.com/spanditime/rukovoditel-go/schema"
)

func browseItems(ctx context.Context, client *api.Client, scanner *bufio.Scanner, out io.Writer, snapshot schema.Schema, rowsPerPage, page int) (bool, error) {
	entityID := snapshot.Entity.ID
	heading, hasHeading := snapshot.HeadingField()
	for {
		rows, err := selectItems(ctx, client, entityID, map[string]any{"rows_per_page": rowsPerPage, "page": page})
		if err != nil {
			return false, err
		}
		available := make(map[int64]bool, len(rows))
		fmt.Fprintf(out, "ITEMS entity=%d rows=%d rows_per_page=%d page=%d\n", entityID, len(rows), rowsPerPage, page)
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		if hasHeading {
			fmt.Fprintf(table, "ITEM ID\t%q (field %d)\n", heading.ShortNameOrName(), heading.ID)
		} else {
			fmt.Fprintln(table, "ITEM ID")
		}
		for _, row := range rows {
			id, ok := rowID(row)
			if !ok {
				if hasHeading {
					fmt.Fprintf(table, "<ID unavailable>\t%s\n", itemFieldValue(row, heading.ID))
				} else {
					fmt.Fprintln(table, "<ID unavailable>")
				}
				continue
			}
			available[id] = true
			if hasHeading {
				fmt.Fprintf(table, "%d\t%s\n", id, itemFieldValue(row, heading.ID))
			} else {
				fmt.Fprintln(table, id)
			}
		}
		if err := table.Flush(); err != nil {
			return false, err
		}
		fmt.Fprintln(out, "Only one server response is shown; additional items may exist. Pages beyond the result set may repeat.")
		for {
			fmt.Fprint(out, "Item ID (n=next, p=previous, r=reload, b=fields, q=quit): ")
			if !scanner.Scan() {
				return true, scanner.Err()
			}
			choice := strings.TrimSpace(scanner.Text())
			switch choice {
			case "q":
				return true, nil
			case "b":
				return false, nil
			case "r":
				break
			case "n":
				if page == math.MaxInt {
					fmt.Fprintln(out, "Page number is too large.")
					continue
				}
				page++
				break
			case "p":
				if page == 1 {
					fmt.Fprintln(out, "Already on page 1.")
					continue
				}
				page--
				break
			default:
				id, err := strconv.ParseInt(choice, 10, 64)
				if err != nil || id <= 0 {
					fmt.Fprintln(out, "Enter a positive item ID, n, p, r, b, or q.")
					continue
				}
				if !available[id] {
					fmt.Fprintf(out, "Item %d is absent from this response; use -item-id to open a known ID directly.\n", id)
					continue
				}
				if err := showItem(ctx, client, out, snapshot, id); err != nil {
					return false, err
				}
				continue
			}
			break
		}
	}
}

func itemFieldValue(raw json.RawMessage, fieldID int64) string {
	var row map[string]json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil {
		return "<unavailable>"
	}
	value, ok := row[strconv.FormatInt(fieldID, 10)]
	if !ok {
		return "<absent>"
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, value); err != nil {
		return "<unavailable>"
	}
	return compact.String()
}

func showItem(ctx context.Context, client *api.Client, out io.Writer, snapshot schema.Schema, id int64) error {
	entityID := snapshot.Entity.ID
	rows, err := selectItems(ctx, client, entityID, map[string]any{"filters": map[string]any{"id": id}})
	if err != nil {
		return err
	}
	var found json.RawMessage
	for _, row := range rows {
		gotID, ok := rowID(row)
		if !ok || gotID != id {
			continue
		}
		if found != nil {
			return fmt.Errorf("item %d appears more than once in select response", id)
		}
		found = row
	}
	if found == nil {
		return fmt.Errorf("item %d absent from select response", id)
	}
	fmt.Fprintf(out, "ITEM %d (entity %d)\n", id, entityID)
	return showItemFields(out, snapshot, found)
}

func showItemFields(out io.Writer, snapshot schema.Schema, raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("decode item fields: %w", err)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i] == "id" || keys[j] == "id" {
			return keys[i] == "id"
		}
		if keys[i] == "id_db_value" || keys[j] == "id_db_value" {
			return keys[i] == "id_db_value"
		}
		leftID, leftDB, leftKnown := itemFieldID(keys[i])
		rightID, rightDB, rightKnown := itemFieldID(keys[j])
		if leftKnown != rightKnown {
			return !leftKnown
		}
		if leftKnown && leftID != rightID {
			return leftID < rightID
		}
		if leftKnown && leftDB != rightDB {
			return !leftDB
		}
		return keys[i] < keys[j]
	})
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tSHORT NAME OR NAME\tVALUE\tIS REQUIRED")
	for _, key := range keys {
		name := key
		required := "-"
		if id, _, ok := itemFieldID(key); ok {
			if meta, found := snapshot.Fields[id]; found {
				name = meta.ShortNameOrName()
				required = boolMark(meta.IsRequired)
			}
		}
		var value bytes.Buffer
		if err := json.Compact(&value, fields[key]); err != nil {
			return fmt.Errorf("format item field %s: %w", key, err)
		}
		fmt.Fprintf(table, "%s\t%q\t%s\t%s\n", key, name, value.String(), required)
	}
	return table.Flush()
}

func itemFieldID(key string) (int64, bool, bool) {
	base, db := strings.CutSuffix(key, "_db_value")
	id, err := strconv.ParseInt(base, 10, 64)
	return id, db, err == nil && id > 0 && strconv.FormatInt(id, 10) == base
}

func selectItems(ctx context.Context, client *api.Client, entityID int64, params map[string]any) ([]json.RawMessage, error) {
	response, err := client.Do(ctx, api.Request{Action: "select", EntityID: entityID, Params: params})
	if err != nil {
		return nil, fmt.Errorf("select items for entity %d: %w", entityID, err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(response.Data, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("invalid select items response: %v", err)
	}
	return rows, nil
}

func rowID(raw json.RawMessage) (int64, bool) {
	var row map[string]json.RawMessage
	if json.Unmarshal(raw, &row) != nil {
		return 0, false
	}
	value := row["id"]
	if len(value) == 0 {
		return 0, false
	}
	var text string
	if value[0] == '"' {
		if json.Unmarshal(value, &text) != nil {
			return 0, false
		}
	} else {
		text = string(value)
	}
	id, err := strconv.ParseInt(text, 10, 64)
	return id, err == nil && id > 0
}
