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

type linkedUser struct {
	ID   string `rukovoditel:"id"`
	Name string `rukovoditel:"12"`
}

type linkedTask struct {
	ID       int64           `rukovoditel:"id"`
	Owner    linkedUser      `rukovoditel:"420"`
	Approver linkedUser      `rukovoditel:"426"`
	Formula  json.RawMessage `rukovoditel:"432"`
}

type taskWithCreator struct {
	ID      int64      `rukovoditel:"id"`
	Creator linkedUser `rukovoditel:"created_by"`
}

type taskWithChoicesAndQuery struct {
	ID       int64           `rukovoditel:"id"`
	State    schema.Choice   `rukovoditel:"419"`
	Children []*linkedEntity `rukovoditel:"683"`
}

type taskWithQueryIDs struct {
	ID       int64   `rukovoditel:"id"`
	ChildIDs []int64 `rukovoditel:"683"`
}

func TestRepositoryChoicesAndItemsByQuery(t *testing.T) {
	var selects []url.Values
	choiceCalls := 0
	choiceID := "42"
	condition := `e.parent_item_id=[id]`
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		data := `null`
		switch form.Get("action") {
		case "get_entities":
			data = `[{"id":"39","name":"Tasks"},{"id":"37","name":"Children","path":["39"]}]`
		case "get_entity_fields":
			if form.Get("entity_id") == "37" {
				data = `{"500":{"id":"500","type":"fieldtype_input","name":"Name"}}`
			} else {
				data = `{"419":{"id":"419","type":"fieldtype_dropdown","name":"State"},"683":{"id":"683","type":"fieldtype_items_by_query","name":"Children","configuration":"{\"entity_id\":\"37\",\"where_query\":\"` + condition + `\"}"}}`
			}
		case "get_field_choices":
			choiceCalls++
			data = `[{"id":"42","name":"Open"}]`
		case "select":
			selects = append(selects, form)
			if form.Get("entity_id") == "39" {
				data = `[{"id":"7","419":"Shown state","419_db_value":"` + choiceID + `","683":"Shown children"}]`
			} else if form.Get("filters[parent_item_id]") == "7" {
				data = `[{"id":"1366","parent_item_id":"7"},{"id":"1367","parent_item_id":"7"}]`
			} else {
				data = `[{"id":"` + form.Get("filters[id]") + `","parent_item_id":"7","500":"Child"}]`
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":` + data + `}`)), Header: make(http.Header)}, nil
	})
	client, err := api.NewClient(api.Config{Endpoint: "http://example.test/api", Key: "k", Username: "u", Password: "p", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	cache, _ := schema.NewSchemaCache(client)
	for _, id := range []int64{37, 39} {
		if _, err := cache.Refresh(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	for _, binding := range []struct {
		id    int64
		model any
	}{{37, linkedEntity{}}, {39, taskWithChoicesAndQuery{}}} {
		if _, err := cache.Register(binding.id, binding.model); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := NewRepository[taskWithChoicesAndQuery](client, cache)
	if err != nil {
		t.Fatal(err)
	}
	item, err := repo.Get(context.Background(), 7)
	if err != nil || item.State.ID != 42 || item.State.Name != "Open" || len(item.Children) != 2 || item.Children[0].ID != "1366" || item.Children[1].ID != "1367" {
		t.Fatalf("item = %+v, %v", item, err)
	}
	if choiceCalls != 1 || len(selects) != 4 || selects[1].Get("filters[parent_item_id]") != "7" || selects[1].Get("limit") != "0" {
		t.Fatalf("requests choices=%d selects=%v", choiceCalls, selects)
	}
	choiceID = "43"
	unlisted, err := repo.Get(context.Background(), 7)
	if err != nil || unlisted.State.ID != 43 || unlisted.State.Name != "" || unlisted.State.Raw != nil {
		t.Fatalf("unlisted choice = %+v, %v", unlisted.State, err)
	}
	condition = `[123]=[id] and [456]>0`
	if _, err := cache.Refresh(context.Background(), 39); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Register(39, taskWithQueryIDs{}); err != nil {
		t.Fatal(err)
	}
	idsRepo, err := NewRepository[taskWithQueryIDs](client, cache)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idsRepo.Get(context.Background(), 7); err == nil || !strings.Contains(err.Error(), "requires a resolver") {
		t.Fatalf("complex SQL accepted without resolver: %v", err)
	}
	configured := idsRepo.WithItemsByQueryResolver(func(ctx context.Context, sourceEntityID, sourceItemID int64, field schema.FieldMetadata, targetEntityID int64) ([]int64, error) {
		if sourceEntityID != 39 || sourceItemID != 7 || field.ID != 683 || targetEntityID != 37 {
			t.Fatalf("resolver input = %d %d %d %d", sourceEntityID, sourceItemID, field.ID, targetEntityID)
		}
		return []int64{1366, 1367}, nil
	})
	ids, err := configured.Get(context.Background(), 7)
	if err != nil || !reflect.DeepEqual(ids.ChildIDs, []int64{1366, 1367}) || len(selects) != 10 {
		t.Fatalf("resolved IDs = %+v, %v; selects=%v", ids, err, selects)
	}
}

func TestRepositoryLoadsCreatedByUser(t *testing.T) {
	var selects []url.Values
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		data := `null`
		switch form.Get("action") {
		case "get_entities":
			data = `[{"id":"1","name":"Users"},{"id":"39","name":"Tasks"}]`
		case "get_entity_fields":
			if form.Get("entity_id") == "1" {
				data = `{"12":{"id":"12","type":"fieldtype_input","name":"Name"}}`
			} else {
				data = `{}`
			}
		case "select":
			selects = append(selects, form)
			if form.Get("entity_id") == "1" {
				data = `[{"id":"74","12":"Alice"}]`
			} else {
				data = `[{"id":"7","created_by":"74"}]`
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":` + data + `}`)), Header: make(http.Header)}, nil
	})
	client, err := api.NewClient(api.Config{Endpoint: "http://example.test/api", Key: "k", Username: "u", Password: "p", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	cache, _ := schema.NewSchemaCache(client)
	for _, id := range []int64{1, 39} {
		if _, err := cache.Refresh(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	for _, binding := range []struct {
		id    int64
		model any
	}{{1, linkedUser{}}, {39, taskWithCreator{}}} {
		if _, err := cache.Register(binding.id, binding.model); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := NewRepository[taskWithCreator](client, cache)
	if err != nil {
		t.Fatal(err)
	}
	item, err := repo.Get(context.Background(), 7)
	if err != nil || item.Creator.ID != "74" || item.Creator.Name != "Alice" || len(selects) != 2 || selects[1].Get("entity_id") != "1" || selects[1].Get("filters[id]") != "74" {
		t.Fatalf("creator = %+v, %v; selects=%v", item, err, selects)
	}
}

type linkedEntity struct {
	ID   string `rukovoditel:"id"`
	Name string `rukovoditel:"500"`
}

type taskWithEntity struct {
	ID      int64         `rukovoditel:"id"`
	Related *linkedEntity `rukovoditel:"428"`
}

type taskWithRelations struct {
	ID       int64          `rukovoditel:"id"`
	Owners   []*linkedUser  `rukovoditel:"420"`
	Projects []linkedEntity `rukovoditel:"428"`
	Parent   *linkedEntity  `rukovoditel:"parent_item_id"`
}

type taskIDsOnly struct {
	ID        int64   `rukovoditel:"id"`
	OwnerID   int64   `rukovoditel:"420"`
	Approvers []int64 `rukovoditel:"426"`
	ParentID  int64   `rukovoditel:"parent_item_id"`
}

func TestRepositoryIDOnlyRelationsDoNotFetchTargets(t *testing.T) {
	selectCount := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		data := `null`
		switch form.Get("action") {
		case "get_entities":
			data = `[{"id":"39","name":"Tasks","path":["37"]}]`
		case "get_entity_fields":
			data = `{"420":{"id":"420","type":"fieldtype_users"},"426":{"id":"426","type":"fieldtype_users_approve"}}`
		case "select":
			selectCount++
			if form.Get("entity_id") != "39" {
				t.Errorf("unexpected target fetch: %v", form)
			}
			data = `[{"id":"7","parent_item_id":"1366","420":"Shown owner","420_db_value":"74","426":"Shown approvers","426_db_value":"74,75"}]`
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
	if _, err := cache.Register(39, taskIDsOnly{}); err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository[taskIDsOnly](client, cache)
	if err != nil {
		t.Fatal(err)
	}
	item, err := repo.Get(context.Background(), 7)
	if err != nil || item.OwnerID != 74 || !reflect.DeepEqual(item.Approvers, []int64{74, 75}) || item.ParentID != 1366 || selectCount != 1 {
		t.Fatalf("ID-only Get = %+v, %v, selects=%d", item, err, selectCount)
	}
}

func TestRepositoryLoadsManyAndParentRelations(t *testing.T) {
	var selects []url.Values
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		data := `null`
		switch form.Get("action") {
		case "get_entities":
			data = `[{"id":"1","name":"Users"},{"id":"37","name":"Projects"},{"id":"39","name":"Tasks","path":["37"]}]`
		case "get_entity_fields":
			switch form.Get("entity_id") {
			case "1":
				data = `{"12":{"id":"12","type":"fieldtype_input","name":"Name"}}`
			case "37":
				data = `{"500":{"id":"500","type":"fieldtype_input","name":"Name"}}`
			default:
				data = `{"420":{"id":"420","type":"fieldtype_users","name":"Owners"},"428":{"id":"428","type":"fieldtype_entity_ajax","name":"Projects","configuration":"{\"entity_id\":\"37\"}"}}`
			}
		case "select":
			selects = append(selects, form)
			switch form.Get("entity_id") {
			case "1":
				data = `[{"id":"` + form.Get("filters[id]") + `","12":"User"}]`
			case "37":
				data = `[{"id":"` + form.Get("filters[id]") + `","500":"Project"}]`
			default:
				data = `[{"id":"7","parent_item_id":"1366","420":"Shown owners","420_db_value":"74,75","428":"Shown projects","428_db_value":"1366,1367"}]`
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":` + data + `}`)), Header: make(http.Header)}, nil
	})
	client, err := api.NewClient(api.Config{Endpoint: "http://example.test/api", Key: "k", Username: "u", Password: "p", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	cache, _ := schema.NewSchemaCache(client)
	for _, id := range []int64{1, 37, 39} {
		if _, err := cache.Refresh(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	for _, binding := range []struct {
		id    int64
		model any
	}{{1, linkedUser{}}, {37, linkedEntity{}}, {39, taskWithRelations{}}} {
		if _, err := cache.Register(binding.id, binding.model); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := NewRepository[taskWithRelations](client, cache)
	if err != nil {
		t.Fatal(err)
	}
	item, err := repo.Get(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Owners) != 2 || item.Owners[0].ID != "74" || item.Owners[1].ID != "75" || len(item.Projects) != 2 || item.Projects[0].ID != "1366" || item.Projects[1].ID != "1367" || item.Parent == nil || item.Parent.ID != "1366" {
		t.Fatalf("related item = %+v", item)
	}
	if len(selects) != 5 {
		t.Fatalf("select count = %d; want 5", len(selects))
	}
}

func TestRepositoryLoadsEntityAjaxFromDBValue(t *testing.T) {
	var selects []url.Values
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		data := `null`
		switch form.Get("action") {
		case "get_entities":
			data = `[{"id":"37","name":"Projects"},{"id":"39","name":"Tasks"}]`
		case "get_entity_fields":
			if form.Get("entity_id") == "37" {
				data = `{"500":{"id":"500","type":"fieldtype_input","name":"Name"}}`
			} else {
				data = `{"428":{"id":"428","type":"fieldtype_entity_ajax","name":"Project","configuration":"{\"entity_id\":\"37\"}"}}`
			}
		case "select":
			selects = append(selects, form)
			if form.Get("entity_id") == "37" {
				data = `[{"id":"1366","500":"Project"}]`
			} else {
				data = `[{"id":"7","428":"Displayed project","428_db_value":"1366"}]`
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":` + data + `}`)), Header: make(http.Header)}, nil
	})
	client, err := api.NewClient(api.Config{Endpoint: "http://example.test/api", Key: "k", Username: "u", Password: "p", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	cache, _ := schema.NewSchemaCache(client)
	for _, entityID := range []int64{37, 39} {
		if _, err := cache.Refresh(context.Background(), entityID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cache.Register(37, linkedEntity{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Register(39, taskWithEntity{}); err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository[taskWithEntity](client, cache)
	if err != nil {
		t.Fatal(err)
	}
	item, err := repo.Get(context.Background(), 7)
	if err != nil || item.Related == nil || item.Related.ID != "1366" || item.Related.Name != "Project" {
		t.Fatalf("related Get = %+v, %v", item, err)
	}
	if len(selects) != 2 || selects[1].Get("entity_id") != "37" || selects[1].Get("filters[id]") != "1366" {
		t.Fatalf("select forms = %v", selects)
	}
}

func TestRelationTargetID(t *testing.T) {
	tests := []struct {
		name string
		meta schema.FieldMetadata
		want int64
	}{
		{"user", schema.FieldMetadata{Type: "fieldtype_users"}, 1},
		{"approval", schema.FieldMetadata{Type: "fieldtype_users_approve"}, 1},
		{"users ajax", schema.FieldMetadata{Type: "fieldtype_users_ajax"}, 1},
		{"entity", schema.FieldMetadata{Type: "fieldtype_entity_ajax", Raw: json.RawMessage(`{"configuration":"{\"entity_id\":\"37\"}"}`)}, 37},
		{"entity direct", schema.FieldMetadata{Type: "fieldtype_entity", Raw: json.RawMessage(`{"configuration":"{\"entity_id\":\"37\"}"}`)}, 37},
		{"missing entity", schema.FieldMetadata{Type: "fieldtype_entity_ajax", Raw: json.RawMessage(`{"configuration":"{}"}`)}, 0},
		{"malformed config", schema.FieldMetadata{Type: "fieldtype_entity_ajax", Raw: json.RawMessage(`{"configuration":"broken"}`)}, 0},
		{"unsupported", schema.FieldMetadata{Type: "fieldtype_input"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := relationTargetID(tt.meta)
			if tt.want == 0 && err == nil || tt.want != 0 && (err != nil || got != tt.want) {
				t.Fatalf("relation target = %d, %v; want %d", got, err, tt.want)
			}
		})
	}
}

func TestRepositoryLoadsRelatedUserFromDBValue(t *testing.T) {
	var selects []url.Values
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		form, _ := url.ParseQuery(string(body))
		data := `null`
		switch form.Get("action") {
		case "get_entities":
			data = `[{"id":"1","name":"Users"},{"id":"39","name":"Tasks"}]`
		case "get_entity_fields":
			if form.Get("entity_id") == "1" {
				data = `{"12":{"id":"12","type":"fieldtype_input","name":"Name"}}`
			} else {
				data = `{"420":{"id":"420","type":"fieldtype_users","name":"Owner"},"426":{"id":"426","type":"fieldtype_users_approve","name":"Approver"},"432":{"id":"432","type":"fieldtype_formula","name":"Result"}}`
			}
		case "select":
			selects = append(selects, form)
			if form.Get("entity_id") == "1" {
				data = `[{"id":"74","12":"Alice"}]`
			} else {
				data = `[{"id":"7","420":"Displayed user","420_db_value":"74","426":"Displayed approver","426_db_value":"74","432":"computed"}]`
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":` + data + `}`)), Header: make(http.Header)}, nil
	})
	client, err := api.NewClient(api.Config{Endpoint: "http://example.test/api", Key: "k", Username: "u", Password: "p", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	cache, _ := schema.NewSchemaCache(client)
	for _, entityID := range []int64{1, 39} {
		if _, err := cache.Refresh(context.Background(), entityID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cache.Register(1, linkedUser{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Register(39, linkedTask{}); err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository[linkedTask](client, cache)
	if err != nil {
		t.Fatal(err)
	}
	item, err := repo.Get(context.Background(), 7)
	if err != nil || item.Owner.ID != "74" || item.Owner.Name != "Alice" || item.Approver.ID != "74" || string(item.Formula) != `"computed"` {
		t.Fatalf("related Get = %+v, %v", item, err)
	}
	rows, err := repo.Query().RowsPerPage(1).OneResponse(context.Background())
	if err != nil || len(rows) != 1 || rows[0].Owner.Name != "Alice" || rows[0].Approver.Name != "Alice" {
		t.Fatalf("related Query = %+v, %v", rows, err)
	}
	if len(selects) != 4 || selects[0].Get("entity_id") != "39" || selects[1].Get("entity_id") != "1" || selects[1].Get("filters[id]") != "74" || selects[3].Get("filters[id]") != "74" {
		t.Fatalf("select forms = %v", selects)
	}
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
	rows, err := repo.Query().Search("Title", "hello").RowsPerPage(1).Page(2).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != 7 || string(rows[0].Title) != `"hello"` || string(rows[0].Extra["999"]) != "null" {
		t.Fatalf("rows: %#v", rows)
	}
	if forms[0].Get("filters[418][condition]") != "search" || forms[0].Get("filters[418][value]") != "hello" || forms[0].Get("rows_per_page") != "1" || forms[0].Get("page") != "2" {
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
	if _, err := repo.Query().Page(0).All(context.Background()); err == nil {
		t.Fatal("page zero accepted")
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
