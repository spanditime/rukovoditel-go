package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/spanditime/rukovoditel-go/api"
)

func TestInspectBrowsesEntitiesAndFields(t *testing.T) {
	var actions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		action := r.Form.Get("action")
		actions = append(actions, action)
		switch action {
		case "get_entities":
			_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"39","name":"Tasks"},{"id":"1","name":"Users"}]}`))
		case "get_entity_fields":
			if r.Form.Get("entity_id") != "39" {
				t.Errorf("entity_id = %q", r.Form.Get("entity_id"))
			}
			_, _ = w.Write([]byte(`{"status":"success","data":{"419":{"id":"419","name":"State","type":"fieldtype_dropdown"},"418":{"id":"418","name":"Title","type":"fieldtype_input","configuration":"raw"}}}`))
		case "select":
			if r.Form.Get("filters[id]") == "7" {
				_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"7","418":"hello","999":null}]}`))
			} else {
				if r.Form.Get("rows_per_page") != "20" {
					t.Errorf("rows_per_page = %q", r.Form.Get("rows_per_page"))
				}
				_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"7","418":"hello"},{"id":"8","418":"other"}]}`))
			}
		default:
			t.Errorf("unexpected action %q", action)
		}
	}))
	defer server.Close()
	client, err := api.NewClient(api.Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := inspect(context.Background(), client, strings.NewReader("999\n39\ni\n7\nb\n418\nb\nq\n"), &output, 0, 0, 0, 20); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{"1\t\"Users\"", "39\t\"Tasks\"", "Entity 999 is absent", "418\t\"fieldtype_input\"\t\"Title\"", "419\t\"fieldtype_dropdown\"\t\"State\"", `"configuration": "raw"`, "ITEMS entity=39 rows=2", "ITEM 7 (entity 39)", `"999": null`, "additional items may exist"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output: %s", want, got)
		}
	}
	if !reflect.DeepEqual(actions, []string{"get_entities", "get_entities", "get_entity_fields", "select", "select", "get_entities"}) {
		t.Fatalf("actions = %v", actions)
	}
}

func TestInspectDirectField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("action") == "get_entities" {
			_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"39","name":"Tasks"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"418":{"id":"418","name":"Title","type":"fieldtype_input"}}}`))
	}))
	defer server.Close()
	client, err := api.NewClient(api.Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := inspect(context.Background(), client, strings.NewReader(""), &output, 39, 418, 0, 20); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "FIELD 418 (entity 39)") {
		t.Fatal(output.String())
	}
	if err := inspect(context.Background(), client, strings.NewReader(""), &output, 39, 999, 0, 20); err == nil {
		t.Fatal("missing field accepted")
	}
}

func TestInspectDirectItemPreservesRawFields(t *testing.T) {
	tests := []struct {
		name, data string
		wantError  bool
	}{
		{"string ID", `[{"id":"7","418":"hello","999":null}]`, false},
		{"numeric ID", `[{"id":7,"418":"hello","999":null}]`, false},
		{"other ID", `[{"id":"8","418":"hello"}]`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				if r.Form.Get("action") != "select" || r.Form.Get("entity_id") != "39" || r.Form.Get("filters[id]") != "7" {
					t.Errorf("unexpected request: %v", r.Form)
				}
				_, _ = w.Write([]byte(`{"status":"success","data":` + tt.data + `}`))
			}))
			defer server.Close()
			client, err := api.NewClient(api.Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			err = inspect(context.Background(), client, strings.NewReader(""), &output, 39, 0, 7, 20)
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v, wantError = %t", err, tt.wantError)
			}
			if !tt.wantError && (!strings.Contains(output.String(), `"999": null`) || !strings.Contains(output.String(), "ITEM 7 (entity 39)")) {
				t.Fatal(output.String())
			}
		})
	}
}
