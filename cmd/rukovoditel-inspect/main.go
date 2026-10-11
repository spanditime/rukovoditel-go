// Command rukovoditel-inspect browses entities, fields, and items without writes.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spanditime/rukovoditel-go/api"
	"github.com/spanditime/rukovoditel-go/schema"
)

func main() {
	entityID := flag.Int64("entity-id", 0, "show this entity directly")
	fieldID := flag.Int64("field-id", 0, "show this field directly; requires -entity-id")
	itemID := flag.Int64("item-id", 0, "show this item directly; requires -entity-id")
	rowsPerPage := flag.Int("rows-per-page", 20, "size of the single item response")
	flag.Parse()
	if *entityID < 0 || *fieldID < 0 || *itemID < 0 || *rowsPerPage <= 0 ||
		((*fieldID > 0 || *itemID > 0) && *entityID == 0) || (*fieldID > 0 && *itemID > 0) {
		fmt.Fprintln(os.Stderr, "use positive IDs; -field-id and -item-id each require -entity-id and cannot be combined; -rows-per-page must be positive")
		os.Exit(2)
	}
	client, err := api.NewClient(api.Config{
		Endpoint: os.Getenv("RUKOVODITEL_ENDPOINT"),
		Key:      os.Getenv("RUKOVODITEL_KEY"),
		Username: os.Getenv("RUKOVODITEL_USERNAME"),
		Password: os.Getenv("RUKOVODITEL_PASSWORD"),
	})
	if err == nil {
		err = inspect(context.Background(), client, os.Stdin, os.Stdout, *entityID, *fieldID, *itemID, *rowsPerPage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func inspect(ctx context.Context, client *api.Client, in io.Reader, out io.Writer, entityID, fieldID, itemID int64, rowsPerPage int) error {
	cache, err := schema.NewSchemaCache(client)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(in)
	for {
		if entityID == 0 {
			entities, err := cache.ListEntities(ctx)
			if err != nil {
				return fmt.Errorf("list entities: %w", err)
			}
			fmt.Fprintln(out, "ENTITY ID\tNAME")
			available := make(map[int64]bool, len(entities))
			for _, entity := range entities {
				fmt.Fprintf(out, "%d\t%q\n", entity.ID, entity.Name)
				available[entity.ID] = true
			}
			for {
				entityID, err = readID(scanner, out, "Entity ID (q=quit): ")
				if err != nil {
					return err
				}
				if entityID == 0 {
					return nil
				}
				if available[entityID] {
					break
				}
				fmt.Fprintf(out, "Entity %d is absent from the visible list.\n", entityID)
			}
		}
		if itemID > 0 {
			return showItem(ctx, client, out, entityID, itemID)
		}
		snapshot, err := cache.Refresh(ctx, entityID)
		if err != nil {
			return fmt.Errorf("load entity %d: %w", entityID, err)
		}
		fmt.Fprintf(out, "ENTITY %d %q\n", snapshot.Entity.ID, snapshot.Entity.Name)
		fmt.Fprintln(out, "FIELD ID\tTYPE\tNAME")
		ids := make([]int64, 0, len(snapshot.Fields))
		for id := range snapshot.Fields {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			field := snapshot.Fields[id]
			fmt.Fprintf(out, "%d\t%q\t%q\n", id, field.Type, field.Name)
		}
		if fieldID > 0 {
			return showField(out, snapshot, fieldID)
		}
	entityMenu:
		for {
			fmt.Fprint(out, "Field ID or i=items (b=entities, q=quit): ")
			if !scanner.Scan() {
				return scanner.Err()
			}
			choice := strings.TrimSpace(scanner.Text())
			switch choice {
			case "q":
				return nil
			case "b":
				entityID = 0
				break entityMenu
			case "i":
				quit, err := browseItems(ctx, client, scanner, out, entityID, rowsPerPage)
				if err != nil || quit {
					return err
				}
				continue
			}
			id, err := strconv.ParseInt(choice, 10, 64)
			if err != nil || id <= 0 {
				fmt.Fprintln(out, "Enter a positive field ID, i, b, or q.")
				continue
			}
			if err := showField(out, snapshot, id); err != nil {
				fmt.Fprintln(out, err)
			}
		}
	}
}

func readID(scanner *bufio.Scanner, out io.Writer, prompt string) (int64, error) {
	for {
		fmt.Fprint(out, prompt)
		if !scanner.Scan() {
			return 0, scanner.Err()
		}
		value := strings.TrimSpace(scanner.Text())
		if value == "q" {
			return 0, nil
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err == nil && id > 0 {
			return id, nil
		}
		fmt.Fprintln(out, "Enter a positive ID or q.")
	}
}

func showField(out io.Writer, snapshot schema.Schema, id int64) error {
	field, ok := snapshot.Fields[id]
	if !ok {
		return fmt.Errorf("field %d is absent from entity %d metadata", id, snapshot.Entity.ID)
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, field.Raw, "", "  "); err != nil {
		return fmt.Errorf("format field %d metadata: %w", id, err)
	}
	fmt.Fprintf(out, "FIELD %d (entity %d)\n%s\n", id, snapshot.Entity.ID, formatted.String())
	return nil
}
