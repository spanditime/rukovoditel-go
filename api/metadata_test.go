package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestMetadataAndProcessActions(t *testing.T) {
	itemID := int64(1)
	tests := []struct {
		name string
		want url.Values
		call func(*Client) ([]byte, error)
	}{
		{"entities", url.Values{"action": {"get_entities"}}, func(c *Client) ([]byte, error) { return c.GetEntities(context.Background()) }},
		{"fields", url.Values{"action": {"get_entity_fields"}, "entity_id": {"21"}}, func(c *Client) ([]byte, error) { return c.GetEntityFields(context.Background(), 21) }},
		{"field choices", url.Values{"action": {"get_field_choices"}, "entity_id": {"21"}, "field_id": {"157"}}, func(c *Client) ([]byte, error) { return c.GetFieldChoices(context.Background(), 21, 157) }},
		{"buttons", url.Values{"action": {"get_process_buttons"}, "entity_id": {"21"}}, func(c *Client) ([]byte, error) { return c.GetProcessButtons(context.Background(), 21, nil) }},
		{"buttons for item", url.Values{"action": {"get_process_buttons"}, "entity_id": {"21"}, "item_id": {"1"}}, func(c *Client) ([]byte, error) { return c.GetProcessButtons(context.Background(), 21, &itemID) }},
		{"run process", url.Values{"action": {"run_process"}, "entity_id": {"21"}, "item_id": {"1"}, "process_id": {"7"}}, func(c *Client) ([]byte, error) { return c.RunProcess(context.Background(), 21, 1, 7) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Errorf("unexpected method or content type: %s %s", r.Method, r.Header.Get("Content-Type"))
				}
				if err := r.ParseForm(); err != nil {
					t.Errorf("parse form: %v", err)
				}
				want := tt.want
				want.Set("key", "key")
				want.Set("username", "user")
				want.Set("password", "pass")
				if !reflect.DeepEqual(r.PostForm, want) {
					t.Errorf("form = %v, want %v", r.PostForm, want)
				}
				_, _ = w.Write([]byte(`{"status":"success","data":{"unknown":[1,null,"x"]}}`))
			}))
			defer server.Close()
			client, err := NewClient(Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			data, err := tt.call(client)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != `{"unknown":[1,null,"x"]}` {
				t.Errorf("data = %s", data)
			}
		})
	}
}

func TestMetadataActionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"failed","data":{"unrecognized":true}}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{Endpoint: server.URL, Key: "key", Username: "user", Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := client.GetEntities(context.Background())
	var apiErr *APIError
	if len(data) != 0 || !errors.As(err, &apiErr) || string(apiErr.Data) != `{"unrecognized":true}` {
		t.Errorf("data = %s, error = %v", data, err)
	}
}
