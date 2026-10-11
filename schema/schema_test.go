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
			_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"39","name":"Task","extra":{"x":1}}]}`))
			return
		}
		if v == 1 {
			_, _ = w.Write([]byte(`{"status":"success","data":{"418":{"id":"418","type":"fieldtype_input","name":"Title","is_required":"1","extra":[null,2]}}}`))
		} else {
			_, _ = w.Write([]byte(`{"status":"success","data":{"419":{"id":"419","type":"fieldtype_dropdown","name":"State"}}}`))
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
	if first.Entity.ID != 39 || !strings.Contains(string(first.Entity.Raw), `"extra"`) || first.Fields[418].Type != "fieldtype_input" || !strings.Contains(string(first.Fields[418].Raw), `"extra"`) {
		t.Fatalf("invalid snapshot: %+v", first)
	}
	first.Fields[418] = FieldMetadata{}
	first.Entity.Raw[0] = 'x'
	got, ok := cache.Get(39)
	if !ok || got.Fields[418].ID != 418 || got.Entity.Raw[0] != '{' {
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
	if second.Fields[419].ID != 419 {
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

func TestListEntitiesReadsOnlyEntityMetadata(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = r.ParseForm()
		if r.Form.Get("action") != "get_entities" {
			t.Errorf("unexpected action %q", r.Form.Get("action"))
		}
		_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"39","name":"Task","extra":true},{"id":"1","name":"Users"}]}`))
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
	if requests != 1 || len(entities) != 2 || entities[0].ID != 1 || entities[1].ID != 39 || entities[1].Name != "Task" || !strings.Contains(string(entities[1].Raw), `"extra":true`) {
		t.Fatalf("unexpected entities: %#v, requests=%d", entities, requests)
	}
	if _, ok := cache.Get(39); ok {
		t.Fatal("listing populated the schema cache")
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
	if snapshot.Entity.ID != 39 || len(snapshot.Fields) != 4 || snapshot.Fields[428].Type != "fieldtype_entity_ajax" {
		t.Fatalf("unexpected schema: %+v", snapshot)
	}
	if !strings.Contains(string(snapshot.Entity.Raw), `"path"`) || !strings.Contains(string(snapshot.Entity.Raw), `"group_name"`) || !strings.Contains(string(snapshot.Fields[422].Raw), `"configuration"`) {
		t.Fatal("raw metadata lost properties outside the typed subset")
	}
}
