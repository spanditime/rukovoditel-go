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
	"time"
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

func TestModelPrefersDBValueAndFormulaIsReadOnly(t *testing.T) {
	type record struct {
		Title   json.RawMessage            `rukovoditel:"418"`
		Choice  json.RawMessage            `rukovoditel:"419"`
		Formula json.RawMessage            `rukovoditel:"432"`
		Raw     map[string]json.RawMessage `rukovoditel:"-,raw"`
	}
	s := Schema{Entity: EntityMetadata{ID: 39}, Fields: map[int64]FieldMetadata{
		418: {ID: 418, Type: "fieldtype_input"},
		419: {ID: 419, Type: "fieldtype_dropdown"},
		432: {ID: 432, Type: "fieldtype_formula"},
	}}
	m, err := RegisterModel(s, record{})
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]json.RawMessage{
		"418": []byte(`"shown"`), "419": []byte(`"Shown choice"`), "419_db_value": []byte(`"42"`),
		"432": []byte(`"computed"`),
	}
	var got record
	if err := m.Decode(row, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Choice) != `"42"` || string(got.Raw["419"]) != `"Shown choice"` || got.Raw["419_db_value"] != nil {
		t.Fatalf("db value mapping: %+v", got)
	}
	if string(got.Formula) != `"computed"` {
		t.Fatal("formula was not decoded")
	}
	got.Choice = nil
	if _, err := m.EncodeWrite(s, got); err == nil || !strings.Contains(err.Error(), "formula is read-only") {
		t.Fatalf("formula write was accepted: %v", err)
	}
}

func TestModelRelatedStructUsesDBValue(t *testing.T) {
	type user struct {
		ID string `rukovoditel:"id"`
	}
	type task struct {
		Owner user                       `rukovoditel:"420"`
		Raw   map[string]json.RawMessage `rukovoditel:"-,raw"`
	}
	s := Schema{Entity: EntityMetadata{ID: 39}, Fields: map[int64]FieldMetadata{420: {ID: 420, Type: "fieldtype_users"}}}
	m, err := RegisterModel(s, task{})
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]json.RawMessage{"420": []byte(`"Displayed user"`), "420_db_value": []byte(`"74"`)}
	if err := m.Decode(row, &task{}); err == nil {
		t.Fatal("Decode silently skipped a nested relation")
	}
	var got task
	if err := m.DecodeWithRelations(row, &got, func(fieldID, id int64, dst any) error {
		if fieldID != 420 || id != 74 {
			t.Fatalf("relation callback = %d, %d", fieldID, id)
		}
		dst.(*user).ID = "74"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got.Owner.ID != "74" || string(got.Raw["420"]) != `"Displayed user"` || string(got.Raw["420_db_value"]) != `"74"` {
		t.Fatalf("nested mapping: %+v", got)
	}
	for _, bad := range []map[string]json.RawMessage{
		{"420": []byte(`"Displayed user"`)},
		{"420_db_value": []byte(`"74,75"`)},
	} {
		if err := m.DecodeWithRelations(bad, &task{}, nil); err == nil {
			t.Fatalf("accepted unresolved relation: %v", bad)
		}
	}
	if _, err := m.EncodeWrite(s, got); err == nil || !strings.Contains(err.Error(), "relation is read-only") {
		t.Fatalf("relation write was accepted: %v", err)
	}
}

func TestRelationIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
		want []int64
		bad  bool
	}{
		{"empty", []byte(`""`), nil, false},
		{"null", []byte(`null`), nil, false},
		{"zero parent", []byte(`"0"`), nil, false},
		{"single", []byte(`"74"`), []int64{74}, false},
		{"many", []byte(`"74,75"`), []int64{74, 75}, false},
		{"malformed", []byte(`"74,abc"`), nil, true},
		{"array unconfirmed", []byte(`[74,75]`), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := relationIDs(tc.raw)
			if (err != nil) != tc.bad || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("relation IDs = %v, %v; want %v, bad=%t", got, err, tc.want, tc.bad)
			}
		})
	}
}

func TestDefaultFieldsAndDateTime(t *testing.T) {
	type record struct {
		ID        int64      `rukovoditel:"id"`
		Added     time.Time  `rukovoditel:"date_added"`
		Updated   *time.Time `rukovoditel:"date_updated"`
		CreatorID int64      `rukovoditel:"created_by"`
		Due       time.Time  `rukovoditel:"423"`
	}
	s := Schema{Entity: EntityMetadata{ID: 39}, Fields: map[int64]FieldMetadata{423: {ID: 423, Type: "fieldtype_input_date_extra"}}}
	m, err := RegisterModel(s, record{})
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]json.RawMessage{
		"id": []byte(`"7"`), "date_added": []byte(`"12.10.2026 14:35"`),
		"date_updated": []byte(`null`), "created_by": []byte(`"74"`), "423": []byte(`"13.10.2026"`),
	}
	var got record
	if err := m.Decode(row, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != 7 || got.CreatorID != 74 || got.Updated != nil || !got.Added.Equal(time.Date(2026, 10, 12, 14, 35, 0, 0, time.UTC)) || !got.Due.Equal(time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("decoded defaults = %+v", got)
	}
	if _, err := m.EncodeWrite(s, got); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("system write accepted: %v", err)
	}
	row["423"] = []byte(`"2026-10-13"`)
	if err := m.Decode(row, &record{}); err == nil || !strings.Contains(err.Error(), "unsupported date/time format") {
		t.Fatalf("unknown date accepted: %v", err)
	}
}

func TestModelChoiceAndItemsByQueryBindings(t *testing.T) {
	type related struct {
		ID string `rukovoditel:"id"`
	}
	type record struct {
		State Choice                     `rukovoditel:"419"`
		Items []related                  `rukovoditel:"683"`
		Raw   map[string]json.RawMessage `rukovoditel:"-,raw"`
	}
	s := Schema{Entity: EntityMetadata{ID: 39}, Fields: map[int64]FieldMetadata{
		419: {ID: 419, Type: "fieldtype_dropdown"}, 683: {ID: 683, Type: "fieldtype_items_by_query"},
	}}
	m, err := RegisterModel(s, record{})
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]json.RawMessage{"419": []byte(`"Shown state"`), "419_db_value": []byte(`"42"`), "683": []byte(`"Shown items"`)}
	if err := m.Decode(row, &record{}); err == nil {
		t.Fatal("lookup model decoded without resolvers")
	}
	var got record
	err = m.DecodeWithLookups(row, &got, LookupResolvers{
		Choice: func(fieldID, id int64) (Choice, error) {
			if fieldID != 419 || id != 42 {
				t.Fatalf("choice callback %d %d", fieldID, id)
			}
			return Choice{ID: id, Name: "Open"}, nil
		},
		ItemsByQuery: func(fieldID int64, dst any) error {
			if fieldID != 683 {
				t.Fatalf("query field %d", fieldID)
			}
			*dst.(*[]related) = []related{{ID: "7"}, {ID: "8"}}
			return nil
		},
	})
	if err != nil || got.State.ID != 42 || got.State.Name != "Open" || len(got.Items) != 2 || got.Items[1].ID != "8" || string(got.Raw["419"]) != `"Shown state"` || string(got.Raw["683"]) != `"Shown items"` {
		t.Fatalf("lookups = %+v, %v", got, err)
	}
}

func TestParseDateTimeFormats(t *testing.T) {
	for _, tc := range []struct {
		wire string
		want bool
	}{
		{`"01/15/2018 10:42"`, true}, {`"12.10.2026 14:35"`, true}, {`"12.10.2026"`, true}, {`""`, false}, {`null`, false},
	} {
		_, present, err := parseDateTime([]byte(tc.wire))
		if err != nil || present != tc.want {
			t.Fatalf("parse %s = %t, %v", tc.wire, present, err)
		}
	}
}

func TestModelIDOnlyRelations(t *testing.T) {
	type record struct {
		Owner   int64                      `rukovoditel:"420"`
		Members []string                   `rukovoditel:"426"`
		Items   []int64                    `rukovoditel:"428"`
		Parent  string                     `rukovoditel:"parent_item_id"`
		Raw     map[string]json.RawMessage `rukovoditel:"-,raw"`
	}
	s := Schema{Entity: EntityMetadata{ID: 39, Path: []int64{37}}, Fields: map[int64]FieldMetadata{
		420: {ID: 420, Type: "fieldtype_users"},
		426: {ID: 426, Type: "fieldtype_users_approve"},
		428: {ID: 428, Type: "fieldtype_entity_ajax"},
	}}
	m, err := RegisterModel(s, record{})
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]json.RawMessage{
		"420": []byte(`"display owner"`), "420_db_value": []byte(`"74"`),
		"426": []byte(`"display members"`), "426_db_value": []byte(`"74,75"`),
		"428": []byte(`"display items"`), "428_db_value": []byte(`"1366,1367"`),
		"parent_item_id": []byte(`"1366"`),
	}
	var got record
	if err := m.Decode(row, &got); err != nil {
		t.Fatal(err)
	}
	if got.Owner != 74 || !reflect.DeepEqual(got.Members, []string{"74", "75"}) || !reflect.DeepEqual(got.Items, []int64{1366, 1367}) || got.Parent != "1366" || string(got.Raw["420"]) != `"display owner"` || got.Raw["420_db_value"] != nil {
		t.Fatalf("ID-only decode = %+v", got)
	}
	if _, err := m.EncodeWrite(s, got); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("ID-only write accepted: %v", err)
	}
	row["420_db_value"] = []byte(`"74,75"`)
	if err := m.Decode(row, &record{}); err == nil || !strings.Contains(err.Error(), "multiple IDs") {
		t.Fatalf("scalar accepted multiple IDs: %v", err)
	}
	delete(row, "420_db_value")
	if err := m.Decode(row, &record{}); err == nil || !strings.Contains(err.Error(), "no _db_value") {
		t.Fatalf("missing db value accepted: %v", err)
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
		{"unsupported scalar", struct {
			X int `rukovoditel:"418"`
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
