// Command facade demonstrates read-only access to two models through one connection.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/spanditime/rukovoditel-go"
	"github.com/spanditime/rukovoditel-go/api"
)

// Replace entity and field IDs with IDs from your own installation.
const (
	taskEntityID int64 = 39
	noteEntityID int64 = 40
)

type Task struct {
	ID    int64                      `rukovoditel:"id"`
	Title json.RawMessage            `rukovoditel:"418"`
	Extra map[string]json.RawMessage `rukovoditel:"-,raw"`
}

type Note struct {
	ID    int64                      `rukovoditel:"id"`
	Text  json.RawMessage            `rukovoditel:"519"`
	Extra map[string]json.RawMessage `rukovoditel:"-,raw"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	taskID := flag.Int64("task-id", 0, "ID of an existing task")
	noteID := flag.Int64("note-id", 0, "ID of an existing note")
	flag.Parse()
	if *taskID <= 0 || *noteID <= 0 {
		return fmt.Errorf("-task-id and -note-id must be positive")
	}

	conn, err := rukovoditel.New(api.Config{
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

	for _, entityID := range []int64{taskEntityID, noteEntityID} {
		if _, err := conn.Refresh(ctx, entityID); err != nil {
			return fmt.Errorf("refresh entity %d: %w", entityID, err)
		}
	}
	if _, err := conn.Register(taskEntityID, Task{}); err != nil {
		return err
	}
	if _, err := conn.Register(noteEntityID, Note{}); err != nil {
		return err
	}

	tasks, err := rukovoditel.Repository[Task](conn)
	if err != nil {
		return err
	}
	notes, err := rukovoditel.Repository[Note](conn)
	if err != nil {
		return err
	}
	task, err := tasks.Get(ctx, *taskID)
	if err != nil {
		return err
	}
	note, err := notes.Get(ctx, *noteID)
	if err != nil {
		return err
	}
	fmt.Printf("task id=%d title=%s\n", task.ID, task.Title)
	fmt.Printf("note id=%d text=%s\n", note.ID, note.Text)
	// conn.Client() exposes the same low-level API client for other actions.
	return nil
}
