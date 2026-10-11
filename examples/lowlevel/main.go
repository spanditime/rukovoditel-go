// Command lowlevel runs one explicitly requested Rukovoditel action and prints
// the returned JSON without interpreting action-specific data.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spanditime/rukovoditel-go/api"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	action := flag.String("action", "", "API action to call")
	entityID := flag.Int64("entity-id", 0, "entity ID, if needed by the action")
	paramsFile := flag.String("params-file", "", "path to a JSON object of additional action parameters")
	allowWrite := flag.Bool("allow-write", false, "allow insert, update, delete, or run_process on a test server")
	flag.Parse()

	if strings.TrimSpace(*action) == "" {
		return fmt.Errorf("-action is required")
	}
	if *entityID < 0 {
		return fmt.Errorf("-entity-id must be nonnegative")
	}
	switch *action {
	case "insert", "update", "delete", "run_process":
		if !*allowWrite {
			return fmt.Errorf("%s requires -allow-write", *action)
		}
	}

	var params map[string]any
	if *paramsFile != "" {
		file, err := os.Open(*paramsFile)
		if err != nil {
			return fmt.Errorf("open params file: %w", err)
		}
		defer file.Close()
		decoder := json.NewDecoder(file)
		decoder.UseNumber()
		if err := decoder.Decode(&params); err != nil {
			return fmt.Errorf("decode params file: %w", err)
		}
		if params == nil {
			return fmt.Errorf("params file must contain a JSON object")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return fmt.Errorf("params file must contain exactly one JSON object")
		}
		for key, value := range params {
			params[key] = formValue(value)
		}
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
	response, err := client.Do(ctx, api.Request{Action: *action, EntityID: *entityID, Params: params})
	if err != nil {
		var httpErr *api.HTTPError
		var apiErr *api.APIError
		switch {
		case errors.As(err, &httpErr):
			fmt.Fprintf(os.Stdout, "HTTP %d\n%s\n", httpErr.StatusCode, httpErr.Body)
		case errors.As(err, &apiErr):
			fmt.Fprintf(os.Stdout, "%s\n", apiErr.Body)
		}
		return err
	}
	output, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return fmt.Errorf("format response: %w", err)
	}
	fmt.Println(string(output))
	return nil
}

// formValue retains JSON numbers as decimal strings for the form encoder.
func formValue(value any) any {
	switch v := value.(type) {
	case json.Number:
		return v.String()
	case map[string]any:
		for key, child := range v {
			v[key] = formValue(child)
		}
	case []any:
		for i, child := range v {
			v[i] = formValue(child)
		}
	}
	return value
}
