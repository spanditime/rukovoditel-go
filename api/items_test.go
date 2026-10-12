package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func crudClient(t *testing.T, want url.Values, response string) (*Client, *int) {
	t.Helper()
	calls := new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("unexpected request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		got, err := url.ParseQuery(string(body))
		if err != nil {
			t.Error(err)
			return
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("form = %v, want %v", got, want)
		}
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(testConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	return client, calls
}

func baseCRUDForm(action string) url.Values {
	return url.Values{
		"key": {"secret-key"}, "username": {"secret-user"}, "password": {"secret-password"},
		"action": {action}, "entity_id": {"34"},
	}
}

func TestInsert(t *testing.T) {
	want := baseCRUDForm("insert")
	want.Set("items[field_338]", "Заявка Тест")
	want.Set("items[field_426]", "2017-12-29")
	want.Set("items[field_429]", "166,167")
	want.Set("items[parent_item_id]", "12")
	client, _ := crudClient(t, want, `{"status":"success","data":{"id":119}}`)
	got, err := client.Insert(context.Background(), 34, map[string]any{
		"field_338": "Заявка Тест", "field_426": "2017-12-29", "field_429": "166,167", "parent_item_id": 12,
	})
	if err != nil || got.ID != 119 {
		t.Fatalf("Insert = %+v, %v", got, err)
	}
}

func TestInsertIDResponseFormats(t *testing.T) {
	for _, tc := range []struct {
		name      string
		data      string
		wantID    int64
		wantError bool
	}{
		{"string ID observed on 3.7", `{"id":"119"}`, 119, false},
		{"number in documented example", `{"id":119}`, 119, false},
		{"missing ID", `{}`, 0, true},
		{"null ID", `{"id":null}`, 0, true},
		{"nondecimal ID", `{"id":"not-an-id"}`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := baseCRUDForm("insert")
			want.Set("items[field_418]", "probe")
			client, _ := crudClient(t, want, `{"status":"success","data":`+tc.data+`}`)
			got, err := client.Insert(context.Background(), 34, map[string]any{"field_418": "probe"})
			if (err != nil) != tc.wantError || got.ID != tc.wantID {
				t.Fatalf("Insert = %+v, %v", got, err)
			}
		})
	}
}

func TestSelect(t *testing.T) {
	limit, rows := 0, 25
	tests := []struct {
		name    string
		filters map[string]any
		want    url.Values
	}{
		{"date and system", map[string]any{"159": "2018-05-10,2018-05-16", "id": "13,14"}, url.Values{"filters[159]": {"2018-05-10,2018-05-16"}, "filters[id]": {"13,14"}}},
		{"open date", map[string]any{"159": "2018-05-10,"}, url.Values{"filters[159]": {"2018-05-10,"}}},
		{"list", map[string]any{"157": map[string]any{"value": 37, "condition": "include"}}, url.Values{"filters[157][value]": {"37"}, "filters[157][condition]": {"include"}}},
		{"exclude", map[string]any{"157": map[string]any{"value": 37, "condition": "exclude"}}, url.Values{"filters[157][value]": {"37"}, "filters[157][condition]": {"exclude"}}},
		{"comparison", map[string]any{"203": ">30"}, url.Values{"filters[203]": {">30"}}},
		{"empty", map[string]any{"203": map[string]any{"condition": "empty_value"}}, url.Values{"filters[203][condition]": {"empty_value"}}},
		{"not empty", map[string]any{"203": map[string]any{"condition": "not_empty_value"}}, url.Values{"filters[203][condition]": {"not_empty_value"}}},
		{"search", map[string]any{"158": map[string]any{"value": "keywords", "condition": "search"}}, url.Values{"filters[158][value]": {"keywords"}, "filters[158][condition]": {"search"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := baseCRUDForm("select")
			want.Set("limit", "0")
			want.Set("rows_per_page", "25")
			want.Set("select_fields", "338,388")
			for key, values := range tc.want {
				want[key] = values
			}
			client, _ := crudClient(t, want, `{"status":"success","data":[{"id":112,"338":"Заявка 6","388":"Новая"}]}`)
			got, err := client.Select(context.Background(), 34, SelectParams{
				Limit: &limit, RowsPerPage: &rows,
				SelectFields: []int64{338, 388}, Filters: tc.filters,
			})
			if err != nil || len(got) != 1 || got[0]["338"] != "Заявка 6" || got[0]["field_338"] != nil {
				t.Fatalf("Select = %v, %v", got, err)
			}
		})
	}
}

func TestSelectRejectsMissingOrWrongData(t *testing.T) {
	for _, data := range []string{"null", "{}", `"text"`} {
		t.Run(data, func(t *testing.T) {
			client, _ := crudClient(t, baseCRUDForm("select"), `{"status":"success","data":`+data+`}`)
			if _, err := client.Select(context.Background(), 34, SelectParams{}); err == nil {
				t.Fatal("Select accepted non-array data")
			}
		})
	}
}

func TestUpdateDeleteCriteria(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		criteria     map[string]any
		wire         url.Values
	}{
		{"update ID", "update", map[string]any{"id": 37}, url.Values{"update_by_field[id]": {"37"}}},
		{"update IDs", "update", map[string]any{"id": []int{37, 38}}, url.Values{"update_by_field[id][0]": {"37"}, "update_by_field[id][1]": {"38"}}},
		{"update field", "update", map[string]any{"field_156": 34}, url.Values{"update_by_field[field_156]": {"34"}}},
		{"delete ID", "delete", map[string]any{"id": 37}, url.Values{"delete_by_field[id]": {"37"}}},
		{"delete IDs", "delete", map[string]any{"id": []int{37, 38}}, url.Values{"delete_by_field[id][0]": {"37"}, "delete_by_field[id][1]": {"38"}}},
		{"delete field", "delete", map[string]any{"field_156": 34}, url.Values{"delete_by_field[field_156]": {"34"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := baseCRUDForm(tc.action)
			for key, values := range tc.wire {
				want[key] = values
			}
			if tc.action == "update" {
				want.Set("data[field_338]", "Изменено")
				want.Set("data[parent_item_id]", "12")
			}
			client, calls := crudClient(t, want, `{"status":"success","data":{"unknown":"shape"}}`)
			var err error
			if tc.action == "update" {
				err = client.Update(context.Background(), 34, map[string]any{"field_338": "Изменено", "parent_item_id": 12}, tc.criteria)
			} else {
				err = client.Delete(context.Background(), 34, tc.criteria)
			}
			if err != nil || *calls != 1 {
				t.Fatalf("%s: calls=%d error=%v", tc.action, *calls, err)
			}
		})
	}
}

func TestUpdateDeleteRejectEmptyCriteria(t *testing.T) {
	for _, criteria := range []map[string]any{nil, {}, {"id": []int{}}, {"id": ""}, {"id": nil}} {
		client, calls := crudClient(t, nil, `{"status":"success","data":null}`)
		if err := client.Update(context.Background(), 34, map[string]any{"field_338": "value"}, criteria); err == nil {
			t.Errorf("Update accepted %v", criteria)
		}
		if err := client.Delete(context.Background(), 34, criteria); err == nil {
			t.Errorf("Delete accepted %v", criteria)
		}
		if *calls != 0 {
			t.Errorf("empty criteria sent %d requests", *calls)
		}
	}
}

func TestSelectPageWire(t *testing.T) {
	page, rows := 2, 2
	want := baseCRUDForm("select")
	want.Set("entity_id", "39")
	want.Set("page", "2")
	want.Set("rows_per_page", "2")
	client, calls := crudClient(t, want, `{"status":"success","data":[]}`)
	_, err := client.Select(context.Background(), 39, SelectParams{Page: &page, RowsPerPage: &rows})
	if err != nil || *calls != 1 {
		t.Fatalf("select page: calls=%d error=%v", *calls, err)
	}
	zero := 0
	if _, err := client.Select(context.Background(), 39, SelectParams{Page: &zero}); err == nil {
		t.Fatal("accepted page zero")
	}
	if *calls != 1 {
		t.Fatalf("invalid page sent HTTP request")
	}
}
