package rukovoditel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spanditime/rukovoditel-go/api"
)

type task struct {
	Title json.RawMessage `rukovoditel:"418"`
}

func TestConnectionPassesLoggerToLayers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		switch r.Form.Get("action") {
		case "get_entities":
			fmt.Fprint(w, `{"status":"success","data":[{"id":"39","name":"Task"}]}`)
		case "get_entity_fields":
			fmt.Fprint(w, `{"status":"success","data":{"418":{"id":"418","type":"fieldtype_input"}}}`)
		case "select":
			fmt.Fprint(w, `{"status":"success","data":[{"id":"7","418":"private-title"}]}`)
		default:
			t.Errorf("unexpected action: %s", r.Form.Get("action"))
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	connection, err := New(api.Config{Endpoint: server.URL, Key: "secret-key", Username: "secret-user", Password: "secret-password", Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if connection.Client().Logger() != logger {
		t.Fatal("configured logger was not passed to API client")
	}
	if _, err := connection.Refresh(context.Background(), 39); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Register(39, task{}); err != nil {
		t.Fatal(err)
	}
	repo, err := Repository[task](connection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	logs := output.String()
	for _, want := range []string{"rukovoditel API request completed", "rukovoditel schema refreshed", "rukovoditel ORM operation completed", "entity_id=39", "operation=get"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("missing %q in logs: %s", want, logs)
		}
	}
	for _, secret := range []string{"secret-key", "secret-user", "secret-password", "private-title", server.URL} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs contain sensitive value %q: %s", secret, logs)
		}
	}
}

type note struct {
	Text json.RawMessage `rukovoditel:"519"`
}

func TestConnectionLifecycle(t *testing.T) {
	fieldType := "fieldtype_input"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		var data string
		switch r.Form.Get("action") {
		case "get_entities":
			data = `[{"id":"39","name":"Task"},{"id":"40","name":"Note"}]`
		case "get_entity_fields":
			switch r.Form.Get("entity_id") {
			case "39":
				data = fmt.Sprintf(`{"418":{"id":"418","type":%q}}`, fieldType)
			case "40":
				data = `{"519":{"id":"519","type":"fieldtype_input"}}`
			default:
				t.Errorf("unexpected entity: %s", r.Form.Get("entity_id"))
				return
			}
		default:
			t.Errorf("unexpected action: %s", r.Form.Get("action"))
			return
		}
		fmt.Fprintf(w, `{"status":"success","data":%s}`, data)
	}))
	defer server.Close()
	c, err := New(api.Config{Endpoint: server.URL, Key: "k", Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatal("New made a request")
	}
	if c.Client() == nil || c.Client() != c.Client() {
		t.Fatal("client not reused")
	}
	if _, err := Repository[task](c); err == nil {
		t.Fatal("unregistered model accepted")
	}
	for _, entity := range []int64{39, 40} {
		if _, err := c.Refresh(context.Background(), entity); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Register(39, task{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Register(40, note{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Register(39, task{}); err == nil {
		t.Fatal("duplicate model accepted")
	}
	for _, check := range []func() error{
		func() error { _, err := Repository[task](c); return err },
		func() error { _, err := Repository[note](c); return err },
	} {
		if err := check(); err != nil {
			t.Fatal(err)
		}
	}
	fieldType = "fieldtype_dropdown"
	if _, err := c.Refresh(context.Background(), 39); err == nil {
		t.Fatal("incompatible refresh accepted")
	}
	fieldType = "fieldtype_input"
	if _, err := c.Refresh(context.Background(), 39); err != nil {
		t.Fatal("old schema was lost:", err)
	}
	if _, err := Repository[task](c); err != nil {
		t.Fatal(err)
	}
}
