package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spanditime/rukovoditel-go/api"
)

func browseItems(ctx context.Context, client *api.Client, scanner *bufio.Scanner, out io.Writer, entityID int64, rowsPerPage int) (bool, error) {
	for {
		rows, err := selectItems(ctx, client, entityID, map[string]any{"rows_per_page": rowsPerPage})
		if err != nil {
			return false, err
		}
		available := make(map[int64]bool, len(rows))
		fmt.Fprintf(out, "ITEMS entity=%d rows=%d rows_per_page=%d\n", entityID, len(rows), rowsPerPage)
		fmt.Fprintln(out, "ITEM ID")
		for _, row := range rows {
			id, ok := rowID(row)
			if !ok {
				fmt.Fprintln(out, "<ID unavailable>")
				continue
			}
			available[id] = true
			fmt.Fprintln(out, id)
		}
		fmt.Fprintln(out, "Only one server response is shown; additional items may exist.")
		for {
			fmt.Fprint(out, "Item ID (r=reload, b=fields, q=quit): ")
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
			default:
				id, err := strconv.ParseInt(choice, 10, 64)
				if err != nil || id <= 0 {
					fmt.Fprintln(out, "Enter a positive item ID, r, b, or q.")
					continue
				}
				if !available[id] {
					fmt.Fprintf(out, "Item %d is absent from this response; use -item-id to open a known ID directly.\n", id)
					continue
				}
				if err := showItem(ctx, client, out, entityID, id); err != nil {
					return false, err
				}
				continue
			}
			break
		}
	}
}

func showItem(ctx context.Context, client *api.Client, out io.Writer, entityID, id int64) error {
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
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, found, "", "  "); err != nil {
		return fmt.Errorf("format item %d: %w", id, err)
	}
	fmt.Fprintf(out, "ITEM %d (entity %d)\n%s\n", id, entityID, formatted.String())
	return nil
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
