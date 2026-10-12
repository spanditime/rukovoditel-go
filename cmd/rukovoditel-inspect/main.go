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
	"text/tabwriter"

	"github.com/spanditime/rukovoditel-go/api"
	"github.com/spanditime/rukovoditel-go/schema"
)

func main() {
	entityID := flag.Int64("entity-id", 0, "show this entity directly")
	fieldID := flag.Int64("field-id", 0, "show this field directly; requires -entity-id")
	itemID := flag.Int64("item-id", 0, "show this item directly; requires -entity-id")
	rowsPerPage := flag.Int("rows-per-page", 20, "size of the single item response")
	page := flag.Int("page", 1, "starting item page (positive)")
	flag.Parse()
	if *entityID < 0 || *fieldID < 0 || *itemID < 0 || *rowsPerPage <= 0 || *page <= 0 ||
		((*fieldID > 0 || *itemID > 0) && *entityID == 0) || (*fieldID > 0 && *itemID > 0) {
		fmt.Fprintln(os.Stderr, "use positive IDs; -field-id and -item-id each require -entity-id and cannot be combined; -rows-per-page and -page must be positive")
		os.Exit(2)
	}
	client, err := api.NewClient(api.Config{
		Endpoint: os.Getenv("RUKOVODITEL_ENDPOINT"),
		Key:      os.Getenv("RUKOVODITEL_KEY"),
		Username: os.Getenv("RUKOVODITEL_USERNAME"),
		Password: os.Getenv("RUKOVODITEL_PASSWORD"),
	})
	if err == nil {
		err = inspectPage(context.Background(), client, os.Stdin, os.Stdout, *entityID, *fieldID, *itemID, *rowsPerPage, *page)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func inspect(ctx context.Context, client *api.Client, in io.Reader, out io.Writer, entityID, fieldID, itemID int64, rowsPerPage int) error {
	return inspectPage(ctx, client, in, out, entityID, fieldID, itemID, rowsPerPage, 1)
}

func inspectPage(ctx context.Context, client *api.Client, in io.Reader, out io.Writer, entityID, fieldID, itemID int64, rowsPerPage, page int) error {
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
			showEntities(out, entities)
			available := make(map[int64]bool, len(entities))
			for _, entity := range entities {
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
		snapshot, err := cache.Refresh(ctx, entityID)
		if err != nil {
			return fmt.Errorf("load entity %d: %w", entityID, err)
		}
		if itemID > 0 {
			return showItem(ctx, client, out, snapshot, itemID)
		}
		showFields(out, snapshot)
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
				quit, err := browseItems(ctx, client, scanner, out, snapshot, rowsPerPage, page)
				if err != nil || quit {
					return err
				}
				showFields(out, snapshot)
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

func showEntities(out io.Writer, entities []schema.EntityMetadata) {
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer table.Flush()
	fmt.Fprintln(table, "ENTITY ID\tNAME")
	byID := make(map[int64]schema.EntityMetadata, len(entities))
	for _, entity := range entities {
		byID[entity.ID] = entity
	}
	children := make(map[int64][]schema.EntityMetadata)
	var roots []schema.EntityMetadata
	var orphans []schema.EntityMetadata
	for _, entity := range entities {
		parent := entity.ParentID()
		if parent == 0 {
			roots = append(roots, entity)
		} else if _, visible := byID[parent]; visible && parent != entity.ID {
			children[parent] = append(children[parent], entity)
		} else {
			orphans = append(orphans, entity)
		}
	}
	less := func(a, b schema.EntityMetadata) bool {
		if a.SortOrder != b.SortOrder {
			return a.SortOrder < b.SortOrder
		}
		return a.ID < b.ID
	}
	sort.Slice(roots, func(i, j int) bool { return less(roots[i], roots[j]) })
	sort.Slice(orphans, func(i, j int) bool {
		if orphans[i].ParentID() != orphans[j].ParentID() {
			return orphans[i].ParentID() < orphans[j].ParentID()
		}
		return less(orphans[i], orphans[j])
	})
	for parent := range children {
		list := children[parent]
		sort.Slice(list, func(i, j int) bool { return less(list[i], list[j]) })
	}
	seen := make(map[int64]bool, len(entities))
	var printBranch func(schema.EntityMetadata, []bool)
	printBranch = func(entity schema.EntityMetadata, laterSiblings []bool) {
		if seen[entity.ID] {
			return
		}
		seen[entity.ID] = true
		var prefix string
		for i := 0; i+1 < len(laterSiblings); i++ {
			later := laterSiblings[i]
			if later {
				prefix += "│   "
			} else {
				prefix += "    "
			}
		}
		if len(laterSiblings) > 0 {
			if laterSiblings[len(laterSiblings)-1] {
				prefix += "├─ "
			} else {
				prefix += "└─ "
			}
		}
		suffix := ""
		if len(laterSiblings) == 0 && entity.ParentID() != 0 {
			suffix = fmt.Sprintf(" [parent %d not shown above]", entity.ParentID())
		}
		fmt.Fprintf(table, "%d\t%s%q%s\n", entity.ID, prefix, entity.Name, suffix)
		childList := children[entity.ID]
		for i, child := range childList {
			printBranch(child, append(laterSiblings, i < len(childList)-1))
		}
	}
	for _, root := range roots {
		printBranch(root, nil)
	}
	for _, orphan := range orphans {
		printBranch(orphan, nil)
	}
	// Preserve visibility even if inconsistent metadata forms a cycle.
	remaining := append([]schema.EntityMetadata(nil), entities...)
	sort.Slice(remaining, func(i, j int) bool { return less(remaining[i], remaining[j]) })
	for _, entity := range remaining {
		printBranch(entity, nil)
	}
}

func showFields(out io.Writer, snapshot schema.Schema) {
	fmt.Fprintf(out, "ENTITY %d %q\n", snapshot.Entity.ID, snapshot.Entity.Name)
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer table.Flush()
	fmt.Fprintln(table, "FIELD ID\tTYPE\tNAME\tSHORT NAME\tHEADING\tREQUIRED")
	ids := make([]int64, 0, len(snapshot.Fields))
	for id := range snapshot.Fields {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		field := snapshot.Fields[id]
		fmt.Fprintf(table, "%d\t%q\t%q\t%q\t%s\t%s\n",
			id, strings.TrimPrefix(field.Type, "fieldtype_"), field.Name, field.ShortName,
			boolMark(field.IsHeading), boolMark(field.IsRequired))
	}
}

func boolMark(value bool) string {
	if value {
		return "■"
	}
	return "·"
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
