// Package orm provides typed item operations over registered schema models.
package orm

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/spanditime/rukovoditel-go/api"
	"github.com/spanditime/rukovoditel-go/schema"
)

// Repository performs verified item operations for a registered Go model.
// Update and Delete can affect multiple records when the server matches an ID criterion broadly.
type Repository[T any] struct {
	client *api.Client
	cache  *schema.SchemaCache
	model  *schema.Model
}

// NewRepository binds a registered model to its API client and schema cache.
func NewRepository[T any](client *api.Client, cache *schema.SchemaCache) (*Repository[T], error) {
	if client == nil || cache == nil {
		return nil, fmt.Errorf("orm: repository requires client and cache")
	}
	var zero T
	m, ok := cache.ModelFor(zero)
	if !ok {
		return nil, fmt.Errorf("orm: model is not registered")
	}
	if reflect.TypeOf(zero).Kind() != reflect.Struct {
		return nil, fmt.Errorf("orm: repository type must be a struct")
	}
	return &Repository[T]{client: client, cache: cache, model: m}, nil
}

// Get reads an item by ID. A missing item returns an error.
func (r *Repository[T]) Get(ctx context.Context, id int64) (T, error) {
	var zero T
	if id <= 0 {
		return zero, fmt.Errorf("orm: item ID must be positive")
	}
	resp, err := r.client.Do(ctx, api.Request{Action: "select", EntityID: r.model.EntityID,
		Params: map[string]any{"filters": map[string]any{"id": id}}})
	if err != nil {
		return zero, err
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &rows); err != nil || rows == nil {
		return zero, fmt.Errorf("orm: invalid select rows: %v", err)
	}
	for _, row := range rows {
		var wireID string
		if err := json.Unmarshal(row["id"], &wireID); err != nil {
			wireID = string(row["id"])
		}
		if wireID == strconv.FormatInt(id, 10) {
			if err := r.model.Decode(row, &zero); err != nil {
				return zero, err
			}
			return zero, nil
		}
	}
	return zero, fmt.Errorf("orm: item %d not found", id)
}

// Insert writes supported fields and returns the created item ID.
func (r *Repository[T]) Insert(ctx context.Context, value T) (int64, error) {
	s, ok := r.cache.Get(r.model.EntityID)
	if !ok {
		return 0, fmt.Errorf("orm: entity schema is not cached")
	}
	data, err := r.model.EncodeWrite(s, value)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, fmt.Errorf("orm: insert data is empty")
	}
	result, err := r.client.Insert(ctx, r.model.EntityID, data)
	return result.ID, err
}

// Update writes supported fields to every item matching the supplied IDs.
func (r *Repository[T]) Update(ctx context.Context, value T, ids ...int64) error {
	criterion, err := idCriterion(ids)
	if err != nil {
		return err
	}
	s, ok := r.cache.Get(r.model.EntityID)
	if !ok {
		return fmt.Errorf("orm: entity schema is not cached")
	}
	data, err := r.model.EncodeWrite(s, value)
	if err != nil {
		return err
	}
	return r.client.Update(ctx, r.model.EntityID, data, criterion)
}

// Delete removes every item matching the supplied IDs.
func (r *Repository[T]) Delete(ctx context.Context, ids ...int64) error {
	criterion, err := idCriterion(ids)
	if err != nil {
		return err
	}
	return r.client.Delete(ctx, r.model.EntityID, criterion)
}

func idCriterion(ids []int64) (map[string]any, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("orm: item IDs are required")
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("orm: item ID must be positive")
		}
	}
	if len(ids) == 1 {
		return map[string]any{"id": ids[0]}, nil
	}
	return map[string]any{"id": ids}, nil
}

// Query selects a single server response page; it does not traverse pages.
type Query[T any] struct {
	repo        *Repository[T]
	filters     map[string]any
	rowsPerPage int
	err         error
}

// Query starts a query for the repository's registered model.
func (r *Repository[T]) Query() *Query[T] { return &Query[T]{repo: r, filters: map[string]any{}} }

// Search adds the documented search filter using a Go struct field name.
func (q *Query[T]) Search(field, value string) *Query[T] {
	if q.err != nil {
		return q
	}
	if strings.TrimSpace(value) == "" {
		q.err = fmt.Errorf("orm: search value is empty")
		return q
	}
	id, ok := q.field(field, "fieldtype_input")
	if !ok {
		return q
	}
	q.filters[strconv.FormatInt(id, 10)] = map[string]any{"value": value, "condition": "search"}
	return q
}

func (q *Query[T]) field(name string, allowed ...string) (int64, bool) {
	id, ok := q.repo.model.FieldID(name)
	if !ok {
		q.err = fmt.Errorf("orm: field %q is not bound", name)
		return 0, false
	}
	s, ok := q.repo.cache.Get(q.repo.model.EntityID)
	if !ok {
		q.err = fmt.Errorf("orm: entity schema is not cached")
		return 0, false
	}
	for _, kind := range allowed {
		if s.Fields[id].Type == kind {
			return id, true
		}
	}
	q.err = fmt.Errorf("orm: field %q has unsupported filter type %q", name, s.Fields[id].Type)
	return 0, false
}

// Empty adds the documented empty or not-empty filter for a bound field.
func (q *Query[T]) Empty(field string, empty bool) *Query[T] {
	if q.err != nil {
		return q
	}
	id, ok := q.field(field, "fieldtype_input", "fieldtype_dropdown", "fieldtype_input_numeric", "fieldtype_input_date_extra")
	if !ok {
		return q
	}
	condition := "not_empty_value"
	if empty {
		condition = "empty_value"
	}
	q.filters[strconv.FormatInt(id, 10)] = map[string]any{"condition": condition}
	return q
}

// Choice adds a documented include or exclude filter for a dropdown option ID.
func (q *Query[T]) Choice(field string, optionID int64, include bool) *Query[T] {
	if q.err != nil {
		return q
	}
	if optionID <= 0 {
		q.err = fmt.Errorf("orm: option ID must be positive")
		return q
	}
	id, ok := q.field(field, "fieldtype_dropdown")
	if !ok {
		return q
	}
	condition := "exclude"
	if include {
		condition = "include"
	}
	q.filters[strconv.FormatInt(id, 10)] = map[string]any{"value": optionID, "condition": condition}
	return q
}

// DateRange filters a date field using YYYY-MM-DD bounds; either bound may be empty.
func (q *Query[T]) DateRange(field, from, to string) *Query[T] {
	if q.err != nil {
		return q
	}
	if from == "" && to == "" {
		q.err = fmt.Errorf("orm: date range is empty")
		return q
	}
	for _, v := range []string{from, to} {
		if v == "" {
			continue
		}
		if len(v) != 10 || strings.ContainsAny(v, ", ") {
			q.err = fmt.Errorf("orm: invalid date bound")
			return q
		}
		if _, err := time.Parse("2006-01-02", v); err != nil {
			q.err = fmt.Errorf("orm: invalid date bound: %w", err)
			return q
		}
	}
	if from != "" && to != "" && from > to {
		q.err = fmt.Errorf("orm: date range is reversed")
		return q
	}
	id, ok := q.field(field, "fieldtype_input_date_extra")
	if !ok {
		return q
	}
	q.filters[strconv.FormatInt(id, 10)] = from + "," + to
	return q
}

// NumericCompare filters a numeric field with one documented comparison operator.
func (q *Query[T]) NumericCompare(field, operator string, value int64) *Query[T] {
	if q.err != nil {
		return q
	}
	if operator != ">" {
		q.err = fmt.Errorf("orm: unsupported comparison operator")
		return q
	}
	id, ok := q.field(field, "fieldtype_input_numeric")
	if !ok {
		return q
	}
	q.filters[strconv.FormatInt(id, 10)] = operator + strconv.FormatInt(value, 10)
	return q
}

// RowsPerPage sets the documented size of the single returned page.
func (q *Query[T]) RowsPerPage(n int) *Query[T] {
	if n <= 0 {
		q.err = fmt.Errorf("orm: rows per page must be positive")
	} else {
		q.rowsPerPage = n
	}
	return q
}

// Sort rejects sorting because its wire format has not been verified.
func (q *Query[T]) Sort(field string) *Query[T] {
	q.err = fmt.Errorf("orm: sorting is unsupported")
	return q
}

// OneResponse executes the query and decodes one server response; it does not traverse pages.
func (q *Query[T]) OneResponse(ctx context.Context) ([]T, error) {
	if q.err != nil {
		return nil, q.err
	}
	params := map[string]any{}
	if len(q.filters) > 0 {
		params["filters"] = q.filters
	}
	if q.rowsPerPage > 0 {
		params["rows_per_page"] = q.rowsPerPage
	}
	resp, err := q.repo.client.Do(ctx, api.Request{Action: "select", EntityID: q.repo.model.EntityID, Params: params})
	if err != nil {
		return nil, err
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("orm: invalid select rows: %v", err)
	}
	result := make([]T, 0, len(rows))
	for _, row := range rows {
		var item T
		if err := q.repo.model.Decode(row, &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// All is an alias for OneResponse. It returns only one server response.
func (q *Query[T]) All(ctx context.Context) ([]T, error) { return q.OneResponse(ctx) }
