package schema

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/spanditime/rukovoditel-go/api"
)

func TestSchemaCacheRefresh(t *testing.T) {
	var mu sync.Mutex
	version := 1
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		v := version
		mu.Unlock()
		if r.Form.Get("action") == "get_entities" {
			_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"39","name":"Task","notes":"Description","sort_order":"6","parent_id":"25","level":1,"path":["25"],"extra":{"x":1}}]}`))
			return
		}
		if v == 1 {
			_, _ = w.Write([]byte(`{"status":"success","data":{"418":{"id":"418","type":"fieldtype_input","name":"Title","short_name":"Short","is_heading":"1","is_required":"0","extra":[null,2]}}}`))
		} else {
			_, _ = w.Write([]byte(`{"status":"success","data":{"419":{"id":"419","type":"fieldtype_dropdown","name":"State","is_required":true,"is_heading":null}}}`))
		}
	}))
	defer server.Close()
	client, err := api.NewClient(api.Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewSchemaCache(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get(39); ok {
		t.Fatal("unexpected cached schema")
	}
	first, err := cache.Refresh(context.Background(), 39)
	if err != nil {
		t.Fatal(err)
	}
	if first.Entity.ID != 39 || first.Entity.Notes != "Description" || first.Entity.SortOrder != 6 || first.Entity.Level() != 1 || first.Entity.ParentID() != 25 || !strings.Contains(string(first.Entity.Raw), `"extra"`) || first.Fields[418].Type != "fieldtype_input" || first.Fields[418].ShortName != "Short" || !first.Fields[418].IsHeading || first.Fields[418].IsRequired || !strings.Contains(string(first.Fields[418].Raw), `"extra"`) {
		t.Fatalf("invalid snapshot: %+v", first)
	}
	first.Fields[418] = FieldMetadata{}
	first.Entity.Raw[0] = 'x'
	first.Entity.Path[0] = 999
	got, ok := cache.Get(39)
	if !ok || got.Fields[418].ID != 418 || got.Entity.Raw[0] != '{' || got.Entity.ParentID() != 25 {
		t.Fatal("cache changed through returned snapshot")
	}
	mu.Lock()
	version = 2
	mu.Unlock()
	second, err := cache.Refresh(context.Background(), 39)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.Fields[418]; ok {
		t.Fatal("removed field persisted")
	}
	if second.Fields[419].ID != 419 || !second.Fields[419].IsRequired || second.Fields[419].IsHeading {
		t.Fatal("new field absent")
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s, ok := cache.Get(39)
				if !ok || s.Fields[419].ID != 419 {
					t.Error("inconsistent snapshot")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestSchemaCacheChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		var data string
		switch r.Form.Get("action") {
		case "get_entities":
			data = `[{"id":"39","name":"Tasks"}]`
		case "get_entity_fields":
			data = `{"419":{"id":"419","type":"fieldtype_dropdown","name":"State"}}`
		case "get_field_choices":
			if r.Form.Get("field_id") != "419" {
				t.Errorf("field ID = %q", r.Form.Get("field_id"))
			}
			data = `[{"id":"42","name":"Open","sort_order":"2","extra":true},{"id":"43","name":"Closed"}]`
		default:
			t.Errorf("unexpected action %q", r.Form.Get("action"))
			data = `null`
		}
		_, _ = w.Write([]byte(`{"status":"success","data":` + data + `}`))
	}))
	defer server.Close()
	client, err := api.NewClient(api.Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	cache, _ := NewSchemaCache(client)
	if _, err := cache.Refresh(context.Background(), 39); err != nil {
		t.Fatal(err)
	}
	choices, err := cache.Choices(context.Background(), 39, 419)
	if err != nil || len(choices) != 2 || choices[0].ID != 42 || choices[0].Name != "Open" || !strings.Contains(string(choices[0].Raw), `"extra":true`) {
		t.Fatalf("choices = %+v, %v", choices, err)
	}
	if _, err := cache.Choices(context.Background(), 39, 999); err == nil {
		t.Fatal("unknown dropdown accepted")
	}
}

func TestMetadataFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{"string one", `"1"`, true},
		{"escaped string one", `"\u0031"`, true},
		{"number one", `1`, true},
		{"boolean true", `true`, true},
		{"string zero", `"0"`, false},
		{"boolean false", `false`, false},
		{"null", `null`, false},
		{"other string", `"yes"`, false},
		{"missing", ``, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := metadataFlag([]byte(tc.raw)); got != tc.want {
				t.Fatalf("metadataFlag(%s) = %t, want %t", tc.raw, got, tc.want)
			}
		})
	}
}

func TestSchemaHeadingField(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[int64]FieldMetadata
		wantID int64
		found  bool
	}{
		{"none", map[int64]FieldMetadata{3: {ID: 3}}, 0, false},
		{"one", map[int64]FieldMetadata{3: {ID: 3, IsHeading: true}}, 3, true},
		{"highest ID", map[int64]FieldMetadata{
			5:  {ID: 5, IsHeading: true, Raw: []byte(`{"id":"5"}`)},
			9:  {ID: 9, IsHeading: true, Raw: []byte(`{"id":"9"}`)},
			12: {ID: 12},
		}, 9, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Schema{Fields: tc.fields}
			got, ok := s.HeadingField()
			if ok != tc.found || (ok && got.ID != tc.wantID) {
				t.Fatalf("HeadingField() = (%+v, %t), want ID %d, found %t", got, ok, tc.wantID, tc.found)
			}
			if ok && len(got.Raw) > 0 {
				got.Raw[0] = 'x'
				if s.Fields[tc.wantID].Raw[0] != '{' {
					t.Fatal("heading field changed the schema")
				}
			}
		})
	}
}

func TestFieldMetadataShortNameOrName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field FieldMetadata
		want  string
	}{
		{"configured short name", FieldMetadata{Name: "Original", ShortName: "Short"}, "Short"},
		{"empty short name", FieldMetadata{Name: "Original", ShortName: ""}, "Original"},
		{"both empty", FieldMetadata{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.field.ShortNameOrName(); got != tc.want {
				t.Fatalf("ShortNameOrName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestListEntitiesReadsOnlyEntityMetadata(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = r.ParseForm()
		if r.Form.Get("action") != "get_entities" {
			t.Errorf("unexpected action %q", r.Form.Get("action"))
		}
		_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"39","name":"Task","notes":"Description","sort_order":"6","path":["1","25"],"extra":true},{"id":"1","name":"Users","parent_id":"0","path":[],"sort_order":"1"}]}`))
	}))
	defer server.Close()
	client, err := api.NewClient(api.Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewSchemaCache(client)
	if err != nil {
		t.Fatal(err)
	}
	entities, err := cache.ListEntities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || len(entities) != 2 || entities[0].ID != 1 || entities[0].Level() != 0 || entities[0].ParentID() != 0 || entities[1].ID != 39 || entities[1].Name != "Task" || entities[1].Notes != "Description" || entities[1].SortOrder != 6 || entities[1].Level() != 2 || entities[1].ParentID() != 25 || !strings.Contains(string(entities[1].Raw), `"extra":true`) {
		t.Fatalf("unexpected entities: %#v, requests=%d", entities, requests)
	}
	if _, ok := cache.Get(39); ok {
		t.Fatal("listing populated the schema cache")
	}
}

func TestListEntitiesRejectsInvalidHierarchyNumbers(t *testing.T) {
	for _, tc := range []struct {
		name, entity string
	}{
		{"bad sort order", `{"id":"39","sort_order":"first","path":[]}`},
		{"bad path ID", `{"id":"39","sort_order":"1","path":["wrong"]}`},
		{"zero path ID", `{"id":"39","sort_order":"1","path":["0"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"status":"success","data":[%s]}`, tc.entity)
			}))
			defer server.Close()
			client, err := api.NewClient(api.Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			cache, _ := NewSchemaCache(client)
			if _, err := cache.ListEntities(context.Background()); err == nil {
				t.Fatal("accepted invalid entity metadata")
			}
		})
	}
}

func TestSchemaCacheFailedRefreshKeepsSnapshot(t *testing.T) {
	bad := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("action") == "get_entities" {
			_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"39","name":"Task"}]}`))
		} else if bad {
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"418":{"id":"999"}}}`)
		} else {
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"418":{"id":"418","type":"fieldtype_input"}}}`)
		}
	}))
	defer server.Close()
	client, _ := api.NewClient(api.Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
	cache, _ := NewSchemaCache(client)
	if _, err := cache.Refresh(context.Background(), 39); err != nil {
		t.Fatal(err)
	}
	bad = true
	if _, err := cache.Refresh(context.Background(), 39); err == nil {
		t.Fatal("expected malformed metadata error")
	}
	if s, ok := cache.Get(39); !ok || s.Fields[418].ID != 418 {
		t.Fatal("failed refresh changed cache")
	}
	if _, err := cache.Refresh(context.Background(), 40); err == nil {
		t.Fatal("expected absent entity error")
	}
}

func TestSchemaCacheRefreshAll(t *testing.T) {
	var mu sync.Mutex
	version := 1
	failed := false
	entityCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		defer mu.Unlock()
		switch r.Form.Get("action") {
		case "get_entities":
			entityCalls++
			if version == 1 {
				_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"1","name":"One"},{"id":"2","name":"Two"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"2","name":"Two updated"},{"id":"3","name":"Three"}]}`))
			}
		case "get_entity_fields":
			id := r.Form.Get("entity_id")
			if failed && id == "3" {
				_, _ = w.Write([]byte(`{"status":"failed","data":{"reason":"test"}}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"%s":{"id":"%s","type":"fieldtype_input","name":"Field"}}}`, id, id)
		default:
			t.Errorf("unexpected action %q", r.Form.Get("action"))
		}
	}))
	defer server.Close()
	client, err := api.NewClient(api.Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewSchemaCache(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.RefreshAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	calls := entityCalls
	mu.Unlock()
	if calls != 1 {
		t.Fatalf("get_entities calls = %d, want 1", calls)
	}
	for _, id := range []int64{1, 2} {
		s, ok := cache.Get(id)
		if !ok || s.Fields[id].ID != id {
			t.Fatalf("entity %d not fully loaded", id)
		}
	}
	mu.Lock()
	version = 2
	failed = true
	mu.Unlock()
	if err := cache.RefreshAll(context.Background()); err == nil {
		t.Fatal("expected API error")
	}
	if _, ok := cache.Get(1); !ok {
		t.Fatal("partial replacement removed entity 1")
	}
	if _, ok := cache.Get(3); ok {
		t.Fatal("partial replacement added entity 3")
	}
	mu.Lock()
	failed = false
	mu.Unlock()
	if err := cache.RefreshAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get(1); ok {
		t.Fatal("removed entity persisted")
	}
	if s, ok := cache.Get(2); !ok || s.Entity.Name != "Two updated" {
		t.Fatal("entity 2 not updated")
	}
	if s, ok := cache.Get(3); !ok || s.Fields[3].ID != 3 {
		t.Fatal("entity 3 not added")
	}
}

func TestSchemaCacheServer37Fixtures(t *testing.T) {
	entities, err := os.ReadFile("testdata/entities_3_7.json")
	if err != nil {
		t.Fatal(err)
	}
	fields, err := os.ReadFile("testdata/fields_39_3_7.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		switch r.Form.Get("action") {
		case "get_entities":
			_, _ = w.Write(entities)
		case "get_entity_fields":
			if r.Form.Get("entity_id") != "39" {
				t.Errorf("entity_id = %q", r.Form.Get("entity_id"))
			}
			_, _ = w.Write(fields)
		default:
			t.Errorf("unexpected action %q", r.Form.Get("action"))
		}
	}))
	defer server.Close()
	client, err := api.NewClient(api.Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewSchemaCache(client)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := cache.Refresh(context.Background(), 39)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Entity.ID != 39 || snapshot.Entity.SortOrder != 6 || snapshot.Entity.Level() != 3 || snapshot.Entity.ParentID() != 3 || len(snapshot.Fields) != 4 || snapshot.Fields[428].Type != "fieldtype_entity_ajax" {
		t.Fatalf("unexpected schema: %+v", snapshot)
	}
	if !strings.Contains(string(snapshot.Entity.Raw), `"path"`) || !strings.Contains(string(snapshot.Entity.Raw), `"group_name"`) || !strings.Contains(string(snapshot.Fields[422].Raw), `"configuration"`) {
		t.Fatal("raw metadata lost properties outside the typed subset")
	}
}
