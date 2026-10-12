package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/spanditime/rukovoditel-go/api"
	"github.com/spanditime/rukovoditel-go/schema"
)

func TestShowEntitiesTreeSortOrder(t *testing.T) {
	entities := []schema.EntityMetadata{
		{ID: 9, Name: "Late root", SortOrder: 8},
		{ID: 6, Name: "Later child", SortOrder: 5, Path: []int64{2}},
		{ID: 4, Name: "Grandchild", SortOrder: 1, Path: []int64{2, 3}},
		{ID: 10, Name: "Great-grandchild", SortOrder: 1, Path: []int64{2, 3, 4}},
		{ID: 12, Name: "Last great-grandchild", SortOrder: 2, Path: []int64{2, 3, 4}},
		{ID: 11, Name: "Last grandchild", SortOrder: 2, Path: []int64{2, 3}},
		{ID: 8, Name: "Child of last", SortOrder: 1, Path: []int64{2, 6}},
		{ID: 3, Name: "First child", SortOrder: 1, Path: []int64{2}},
		{ID: 2, Name: "Early root", SortOrder: 2},
		{ID: 7, Name: "Missing parent", SortOrder: 4, Path: []int64{99}},
		{ID: 5, Name: "Tied child", SortOrder: 1, Path: []int64{2}},
	}
	var output bytes.Buffer
	showEntities(&output, entities)
	rows := [][2]string{
		{"ENTITY ID", "NAME"},
		{"2", "\"Early root\""},
		{"3", "├─ \"First child\""},
		{"4", "│   ├─ \"Grandchild\""},
		{"10", "│   │   ├─ \"Great-grandchild\""},
		{"12", "│   │   └─ \"Last great-grandchild\""},
		{"11", "│   └─ \"Last grandchild\""},
		{"5", "├─ \"Tied child\""},
		{"6", "└─ \"Later child\""},
		{"8", "    └─ \"Child of last\""},
		{"9", "\"Late root\""},
		{"7", "\"Missing parent\" [parent 99 not shown above]"},
	}
	var expected strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&expected, "%-11s%s\n", row[0], row[1])
	}
	want := expected.String()
	if got := output.String(); got != want {
		t.Errorf("showEntities() =\n%s\nwant:\n%s", got, want)
	}
}

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
			_, _ = w.Write([]byte(`{"status":"success","data":{"419":{"id":"419","name":"State","type":"fieldtype_dropdown","short_name":"","is_heading":null,"is_required":true},"418":{"id":"418","name":"Title","short_name":"Task","is_heading":"1","is_required":"0","type":"fieldtype_input","configuration":"raw"}}}`))
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
	for _, want := range []string{"Entity 999 is absent", `"configuration": "raw"`, "ITEMS entity=39 rows=2", "ITEM 7 (entity 39)", "SHORT NAME OR NAME", "999", "additional items may exist"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output: %s", want, got)
		}
	}
	for _, pattern := range []string{
		`(?m)^1 +"Users"$`,
		`(?m)^39 +"Tasks"$`,
		`(?m)^418 +"input" +"Title" +"Task" +■ +·$`,
		`(?m)^419 +"dropdown" +"State" +"" +· +■$`,
		`(?m)^ITEM ID +"Task" \(field 418\)$`,
		`(?m)^7 +"hello"$`,
		`(?m)^8 +"other"$`,
	} {
		if !regexp.MustCompile(pattern).MatchString(got) {
			t.Fatalf("missing row matching %q in output: %s", pattern, got)
		}
	}
	if count := strings.Count(got, "FIELD ID  TYPE"); count != 2 {
		t.Fatalf("field list printed %d times, want twice: %s", count, got)
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
				switch r.Form.Get("action") {
				case "get_entities":
					_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"39","name":"Tasks"}]}`))
				case "get_entity_fields":
					_, _ = w.Write([]byte(`{"status":"success","data":{"418":{"id":"418","name":"Title","short_name":"Task","is_required":"1","type":"fieldtype_input"}}}`))
				case "select":
					if r.Form.Get("entity_id") != "39" || r.Form.Get("filters[id]") != "7" {
						t.Errorf("unexpected select: %v", r.Form)
					}
					_, _ = w.Write([]byte(`{"status":"success","data":` + tt.data + `}`))
				default:
					t.Errorf("unexpected request: %v", r.Form)
				}
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
			if !tt.wantError && (!regexp.MustCompile(`(?m)^999\s+"999"\s+null\s+-`).MatchString(output.String()) || !regexp.MustCompile(`(?m)^418\s+"Task"\s+"hello"\s+■`).MatchString(output.String()) || !strings.Contains(output.String(), "ITEM 7 (entity 39)")) {
				t.Fatal(output.String())
			}
		})
	}
}

func TestShowItemFieldsShowsBaseAndDBValue(t *testing.T) {
	snapshot := schema.Schema{Fields: map[int64]schema.FieldMetadata{
		418: {ID: 418, Name: "Title", ShortName: "Task", IsRequired: true},
		419: {ID: 419, Name: "Choice"},
	}}
	var output bytes.Buffer
	if err := showItemFields(&output, snapshot, []byte(`{"419_db_value":"42","id":"7","419":"Shown choice","418_db_value":"raw","418":"Shown title","999":null}`)); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, pattern := range []string{
		`(?m)^id\s+"id"\s+"7"\s+-`,
		`(?m)^418\s+"Task"\s+"Shown title"\s+■`,
		`(?m)^418_db_value\s+"Task"\s+"raw"\s+■`,
		`(?m)^419\s+"Choice"\s+"Shown choice"\s+·`,
		`(?m)^419_db_value\s+"Choice"\s+"42"\s+·`,
		`(?m)^999\s+"999"\s+null\s+-`,
	} {
		if !regexp.MustCompile(pattern).MatchString(got) {
			t.Fatalf("missing row %q in:\n%s", pattern, got)
		}
	}
	positions := []string{"id", "418", "418_db_value", "419", "419_db_value", "999"}
	last := -1
	for _, key := range positions {
		at := strings.Index(got, "\n"+key+" ")
		if at <= last {
			t.Fatalf("rows out of order: %s", got)
		}
		last = at
	}
}

func TestInspectItemPages(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.Form.Get("action") {
		case "get_entities":
			_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"39","name":"Tasks"}]}`))
		case "get_entity_fields":
			_, _ = w.Write([]byte(`{"status":"success","data":{"418":{"id":"418","name":"Title","short_name":"","is_heading":"1","type":"fieldtype_input"}}}`))
		case "select":
			if r.Form.Get("rows_per_page") != "2" {
				t.Errorf("rows_per_page = %q", r.Form.Get("rows_per_page"))
			}
			pages = append(pages, r.Form.Get("page"))
			_, _ = w.Write([]byte(`{"status":"success","data":[{"id":"` + r.Form.Get("page") + `"}]}`))
		default:
			t.Errorf("unexpected action %q", r.Form.Get("action"))
		}
	}))
	defer server.Close()
	client, err := api.NewClient(api.Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := inspectPage(context.Background(), client, strings.NewReader("i\np\nn\np\nb\nq\n"), &output, 39, 0, 0, 2, 2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pages, []string{"2", "1", "2", "1"}) {
		t.Fatalf("pages = %v", pages)
	}
	if !strings.Contains(output.String(), "page=2") || !strings.Contains(output.String(), "page=1") {
		t.Fatal(output.String())
	}
	if !regexp.MustCompile(`(?m)^ITEM ID +"Title" \(field 418\)$`).MatchString(output.String()) || !regexp.MustCompile(`(?m)^2 +<absent>$`).MatchString(output.String()) {
		t.Fatal(output.String())
	}
}
