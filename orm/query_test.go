package orm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/spanditime/rukovoditel-go/api"
	"github.com/spanditime/rukovoditel-go/schema"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type queryTask struct {
	ID    int64                      `rukovoditel:"id"`
	Title json.RawMessage            `rukovoditel:"418"`
	Extra map[string]json.RawMessage `rukovoditel:"-,raw"`
}

func TestRepositoryWireAndDecode(t *testing.T) {
	var forms []url.Values
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(req.Body)
		f, _ := url.ParseQuery(string(b))
		forms = append(forms, f)
		data := `{}`
		switch f.Get("action") {
		case "get_entities":
			data = `[{"id":"39","name":"Task"}]`
		case "get_entity_fields":
			data = `{"418":{"id":"418","type":"fieldtype_input"}}`
		case "select":
			data = `[{"id":"7","418":"hello","999":null}]`
		case "insert":
			data = `{"id":"8"}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":` + data + `}`)), Header: make(http.Header)}, nil
	})
	client, err := api.NewClient(api.Config{Endpoint: "http://example.test/api", Key: "k", Username: "u", Password: "p", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	cache, _ := schema.NewSchemaCache(client)
	if _, err := cache.Refresh(context.Background(), 39); err != nil {
		t.Fatal(err)
	}
	forms = nil
	if _, err := cache.Register(39, queryTask{}); err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository[queryTask](client, cache)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := repo.Query().Search("Title", "hello").RowsPerPage(1).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != 7 || string(rows[0].Title) != `"hello"` || string(rows[0].Extra["999"]) != "null" {
		t.Fatalf("rows: %#v", rows)
	}
	if forms[0].Get("filters[418][condition]") != "search" || forms[0].Get("filters[418][value]") != "hello" || forms[0].Get("rows_per_page") != "1" {
		t.Fatal(forms[0])
	}
	got, err := repo.Get(context.Background(), 7)
	if err != nil || got.ID != 7 || forms[1].Get("filters[id]") != "7" {
		t.Fatalf("get: %#v %v %v", got, err, forms[1])
	}
	id, err := repo.Insert(context.Background(), queryTask{Title: json.RawMessage(`"new"`)})
	if err != nil || id != 8 {
		t.Fatalf("insert %d %v", id, err)
	}
	if forms[2].Get("items[field_418]") != "new" {
		t.Fatal(forms[2])
	}
	if err := repo.Update(context.Background(), queryTask{Title: json.RawMessage(`"changed"`)}, 7, 8); err != nil {
		t.Fatal(err)
	}
	if forms[3].Get("update_by_field[id][0]") != "7" || forms[3].Get("update_by_field[id][1]") != "8" || forms[3].Get("data[field_418]") != "changed" {
		t.Fatal(forms[3])
	}
	if err := repo.Delete(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if forms[4].Get("delete_by_field[id]") != "7" {
		t.Fatal(forms[4])
	}
	before := len(forms)
	if err := repo.Delete(context.Background()); err == nil {
		t.Fatal("empty delete")
	}
	if err := repo.Update(context.Background(), queryTask{}, 0); err == nil {
		t.Fatal("invalid update")
	}
	if _, err := repo.Query().Sort("Title").All(context.Background()); err == nil {
		t.Fatal("sort accepted")
	}
	if _, err := repo.Query().Search("Missing", "x").All(context.Background()); err == nil {
		t.Fatal("unbound search accepted")
	}
	if len(forms) != before {
		t.Fatal("invalid operation sent request")
	}
	if !reflect.DeepEqual([]string{forms[0].Get("action"), forms[1].Get("action"), forms[2].Get("action"), forms[3].Get("action"), forms[4].Get("action")}, []string{"select", "select", "insert", "update", "delete"}) {
		t.Fatal(forms)
	}
}

func TestQueryFilters(t *testing.T) {
	var sent url.Values
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		data := `[]`
		switch form.Get("action") {
		case "get_entities":
			data = `[{"id":"39","name":"Task"}]`
		case "get_entity_fields":
			data = `{"418":{"id":"418","type":"fieldtype_input"},"419":{"id":"419","type":"fieldtype_dropdown"},"423":{"id":"423","type":"fieldtype_input_date_extra"},"430":{"id":"430","type":"fieldtype_input_numeric"}}`
		case "select":
			sent = form
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":` + data + `}`)), Header: make(http.Header)}, nil
	})
	client, err := api.NewClient(api.Config{Endpoint: "http://example.test/api", Key: "k", Username: "u", Password: "p", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	cache, _ := schema.NewSchemaCache(client)
	if _, err := cache.Refresh(context.Background(), 39); err != nil {
		t.Fatal(err)
	}
	type filterTask struct {
		Title  json.RawMessage `rukovoditel:"418"`
		Choice json.RawMessage `rukovoditel:"419"`
		Date   json.RawMessage `rukovoditel:"423"`
		Number json.RawMessage `rukovoditel:"430"`
	}
	if _, err := cache.Register(39, filterTask{}); err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository[filterTask](client, cache)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		query     *Query[filterTask]
		key, want string
	}{
		{"search", repo.Query().Search("Title", "text"), "filters[418][condition]", "search"},
		{"empty", repo.Query().Empty("Title", true), "filters[418][condition]", "empty_value"},
		{"not empty", repo.Query().Empty("Title", false), "filters[418][condition]", "not_empty_value"},
		{"include", repo.Query().Choice("Choice", 37, true), "filters[419][condition]", "include"},
		{"exclude", repo.Query().Choice("Choice", 37, false), "filters[419][condition]", "exclude"},
		{"date", repo.Query().DateRange("Date", "2018-05-10", ""), "filters[423]", "2018-05-10,"},
		{"numeric", repo.Query().NumericCompare("Number", ">", 30), "filters[430]", ">30"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sent = nil
			rows, err := tt.query.OneResponse(context.Background())
			if err != nil || len(rows) != 0 || sent.Get(tt.key) != tt.want {
				t.Fatalf("rows=%v err=%v form=%v", rows, err, sent)
			}
		})
	}
	sent = nil
	if _, err := repo.Query().Search("Title", "x").Choice("Choice", 37, true).OneResponse(context.Background()); err != nil || sent.Get("filters[418][value]") != "x" || sent.Get("filters[419][value]") != "37" {
		t.Fatalf("multiple conditions: %v %v", err, sent)
	}
	invalid := []*Query[filterTask]{repo.Query().Search("Choice", "x"), repo.Query().Choice("Title", 1, true), repo.Query().Choice("Choice", 0, true), repo.Query().DateRange("Date", "bad", ""), repo.Query().NumericCompare("Number", "=", 2), repo.Query().Empty("Missing", true)}
	for _, q := range invalid {
		sent = nil
		if _, err := q.OneResponse(context.Background()); err == nil || sent != nil {
			t.Fatalf("invalid filter sent request: %v %v", err, sent)
		}
	}
}
