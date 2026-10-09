// Command metadata demonstrates the typed action methods while retaining raw data.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/spanditime/rukovoditel-go/internal/api"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	action := flag.String("action", "", "metadata or process action")
	entityID := flag.Int64("entity-id", 0, "entity ID")
	fieldID := flag.Int64("field-id", 0, "field ID")
	itemID := flag.Int64("item-id", 0, "item ID; optional for get_process_buttons")
	processID := flag.Int64("process-id", 0, "process ID")
	allowWrite := flag.Bool("allow-write", false, "allow run_process on a prepared test item")
	flag.Parse()

	// Validate before creating a client so a typo cannot run a different action.
	switch *action {
	case "get_entities":
	case "get_entity_fields", "get_process_buttons":
		if *entityID <= 0 {
			return fmt.Errorf("-entity-id must be positive")
		}
	case "get_field_choices":
		if *entityID <= 0 || *fieldID <= 0 {
			return fmt.Errorf("-entity-id and -field-id must be positive")
		}
	case "run_process":
		if !*allowWrite {
			return fmt.Errorf("run_process requires -allow-write")
		}
		if *entityID <= 0 || *itemID <= 0 || *processID <= 0 {
			return fmt.Errorf("-entity-id, -item-id and -process-id must be positive")
		}
	default:
		return fmt.Errorf("unknown -action %q", *action)
	}
	if *itemID < 0 {
		return fmt.Errorf("-item-id must be nonnegative")
	}

	client, err := api.NewClient(api.Config{
		Endpoint: os.Getenv("RUKOVODITEL_ENDPOINT"),
		Key:      os.Getenv("RUKOVODITEL_KEY"),
		Username: os.Getenv("RUKOVODITEL_USERNAME"),
		Password: os.Getenv("RUKOVODITEL_PASSWORD"),
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var data json.RawMessage
	switch *action {
	case "get_entities":
		data, err = client.GetEntities(ctx)
	case "get_entity_fields":
		data, err = client.GetEntityFields(ctx, *entityID)
	case "get_field_choices":
		data, err = client.GetFieldChoices(ctx, *entityID, *fieldID)
	case "get_process_buttons":
		var optionalItemID *int64
		if *itemID > 0 {
			optionalItemID = itemID
		}
		data, err = client.GetProcessButtons(ctx, *entityID, optionalItemID)
	case "run_process":
		data, err = client.RunProcess(ctx, *entityID, *itemID, *processID)
	}
	if err != nil {
		var httpErr *api.HTTPError
		var apiErr *api.APIError
		switch {
		case errors.As(err, &httpErr):
			return fmt.Errorf("HTTP %d (raw body retained in HTTPError)", httpErr.StatusCode)
		case errors.As(err, &apiErr):
			return fmt.Errorf("API status %q (raw body retained in APIError)", apiErr.Status)
		default:
			return err
		}
	}
	formatted, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("format data: %w", err)
	}
	fmt.Println(string(formatted))
	return nil
}
