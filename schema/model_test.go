package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/spanditime/rukovoditel-go/api"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type taskModel struct {
	ID       int64                      `rukovoditel:"id"`
	Title    json.RawMessage            `rukovoditel:"418"`
	Optional *json.RawMessage           `rukovoditel:"419"`
	Extra    map[string]json.RawMessage `rukovoditel:"-,raw"`
}

func testModelSchema() Schema {
	return Schema{Entity: EntityMetadata{ID: 39}, Fields: map[int64]FieldMetadata{418: {ID: 418, Type: "fieldtype_input"}, 419: {ID: 419, Type: "fieldtype_dropdown"}}}
}

func TestModelRoundTripAndUnknown(t *testing.T) {
	s := testModelSchema()
	m, err := RegisterModel(s, taskModel{})
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]json.RawMessage{"id": json.RawMessage(`"7"`), "418": json.RawMessage(`"hello"`), "419": json.RawMessage(`null`), "428_db_value": json.RawMessage(`{"nested":[1,null]}`)}
	var got taskModel
	if err := m.Decode(row, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != 7 || string(got.Title) != `"hello"` || got.Optional == nil || string(*got.Optional) != "null" || string(got.Extra["428_db_value"]) != `{"nested":[1,null]}` {
		t.Fatalf("decode: %+v", got)
	}
	row["418"][1] = 'X'
	if string(got.Title) != `"hello"` {
		t.Fatal("value aliases source")
	}
	write, err := m.EncodeWrite(s, got)
	if err == nil || !strings.Contains(err.Error(), "419") {
		t.Fatalf("expected unsupported dropdown write, got %v", err)
	}
	got.Optional = nil
	write, err = m.EncodeWrite(s, got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(write, map[string]any{"field_418": "hello"}) {
		t.Fatalf("write: %#v", write)
	}
	got.Title = json.RawMessage(`""`)
	write, err = m.EncodeWrite(s, got)
	if err != nil || write["field_418"] != "" {
		t.Fatalf("empty string: %#v %v", write, err)
	}
	got.Title = json.RawMessage(`null`)
	if _, err = m.EncodeWrite(s, got); err == nil {
		t.Fatal("null accepted")
	}
}

func TestModelValidation(t *testing.T) {
	s := testModelSchema()
	cases := []struct {
		name  string
		value any
	}{
		{"missing", struct {
			X json.RawMessage `rukovoditel:"999"`
		}{}},
		{"duplicate", struct {
			A json.RawMessage `rukovoditel:"418"`
			B json.RawMessage `rukovoditel:"418"`
		}{}},
		{"name", struct {
			X json.RawMessage `rukovoditel:"Title"`
		}{}},
		{"scalar", struct {
			X string `rukovoditel:"418"`
		}{}},
		{"unexported", struct {
			x json.RawMessage `rukovoditel:"418"`
		}{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RegisterModel(s, tc.value); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	m, _ := RegisterModel(s, taskModel{})
	delete(s.Fields, 418)
	if _, err := m.EncodeWrite(s, taskModel{Title: json.RawMessage(`"hello"`)}); err == nil {
		t.Fatal("schema mismatch accepted")
	}
}

func TestRegisteredModelRejectsSchemaDrift(t *testing.T) {
	var fields = `{"418":{"id":"418","type":"fieldtype_input"},"419":{"id":"419","type":"fieldtype_dropdown"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("action") == "get_entities" {
			_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"39","name":"Task"}]}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","data":%s}`, fields)
	}))
	defer server.Close()
	client, err := api.NewClient(api.Config{Endpoint: server.URL, Key: "k", Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewSchemaCache(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cache.Register(39, taskModel{}); err == nil {
		t.Fatal("registered without schema")
	}
	if _, err = cache.Refresh(context.Background(), 39); err != nil {
		t.Fatal(err)
	}
	model, err := cache.Register(39, taskModel{})
	if err != nil {
		t.Fatal(err)
	}
	if model.EntityID != 39 {
		t.Fatal("wrong entity")
	}
	if got, ok := cache.ModelFor((*taskModel)(nil)); !ok || got.EntityID != model.EntityID {
		t.Fatal("type lookup failed")
	}
	if _, err = cache.Register(39, taskModel{}); err == nil {
		t.Fatal("duplicate type accepted")
	}
	fields = `{"419":{"id":"419","type":"fieldtype_dropdown"}}`
	if _, err = cache.Refresh(context.Background(), 39); err == nil || !strings.Contains(err.Error(), "field 418 removed") {
		t.Fatalf("expected removal error, got %v", err)
	}
	if s, ok := cache.Get(39); !ok || s.Fields[418].ID != 418 {
		t.Fatal("failed refresh replaced snapshot")
	}
	fields = `{"418":{"id":"418","type":"fieldtype_dropdown"},"419":{"id":"419","type":"fieldtype_dropdown"}}`
	if err = cache.RefreshAll(context.Background()); err == nil || !strings.Contains(err.Error(), "type changed") {
		t.Fatalf("expected type error, got %v", err)
	}
	if s, ok := cache.Get(39); !ok || s.Fields[418].Type != "fieldtype_input" {
		t.Fatal("failed RefreshAll replaced snapshot")
	}
}
