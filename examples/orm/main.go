// Command orm demonstrates read-only access through a registered Go model.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/spanditime/rukovoditel-go/api"
	"github.com/spanditime/rukovoditel-go/orm"
	"github.com/spanditime/rukovoditel-go/schema"
)

// Task is bound to field 418 of entity 39 on the documented test server.
// Replace these IDs with the IDs from your own Rukovoditel installation.
type Task struct {
	ID    int64                      `rukovoditel:"id"`
	Title json.RawMessage            `rukovoditel:"418"`
	Extra map[string]json.RawMessage `rukovoditel:"-,raw"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	itemID := flag.Int64("item-id", 0, "ID of an existing item in entity 39")
	flag.Parse()
	if *itemID <= 0 {
		return fmt.Errorf("-item-id must be positive")
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

	cache, err := schema.NewSchemaCache(client)
	if err != nil {
		return err
	}
	if _, err := cache.Refresh(ctx, 39); err != nil {
		return err
	}
	if _, err := cache.Register(39, Task{}); err != nil {
		return err
	}
	repo, err := orm.NewRepository[Task](client, cache)
	if err != nil {
		return err
	}
	task, err := repo.Get(ctx, *itemID)
	if err != nil {
		return err
	}
	fmt.Printf("id=%d title=%s\n", task.ID, task.Title)
	var title string
	if err := json.Unmarshal(task.Title, &title); err != nil {
		return err
	}
	// OneResponse reads one server response only. It cannot promise every
	// matching item until page traversal has been verified on this server.
	page, err := repo.Query().Search("Title", title).RowsPerPage(10).OneResponse(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("one response contains %d items\n", len(page))
	return nil
}
