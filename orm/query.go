// Package orm provides typed item operations over registered schema models.
package orm

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spanditime/rukovoditel-go/api"
	"github.com/spanditime/rukovoditel-go/schema"
)

// Repository performs verified item operations for a registered Go model.
// Update and Delete can affect multiple records when the server matches an ID criterion broadly.
type Repository[T any] struct {
	client               *api.Client
	cache                *schema.SchemaCache
	model                *schema.Model
	itemsByQueryResolver ItemsByQueryResolver
}

// ItemsByQueryResolver returns target item IDs for a source row when the
// field's SQL condition cannot be translated to a verified REST filter.
// The field carries its original metadata, including configuration.
type ItemsByQueryResolver func(ctx context.Context, sourceEntityID, sourceItemID int64, field schema.FieldMetadata, targetEntityID int64) ([]int64, error)

// WithItemsByQueryResolver returns a repository copy using resolver for
// items_by_query fields with nontrivial SQL conditions.
func (r *Repository[T]) WithItemsByQueryResolver(resolver ItemsByQueryResolver) *Repository[T] {
	copy := *r
	copy.itemsByQueryResolver = resolver
	return &copy
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
			if err := r.decodeModel(ctx, r.model, row, &zero, newRelatedState()); err != nil {
				return zero, err
			}
			return zero, nil
		}
	}
	return zero, fmt.Errorf("orm: item %d not found", id)
}

type relatedState struct {
	active  map[string]bool
	rows    map[string]map[string]json.RawMessage
	choices map[string]map[int64]schema.Choice
}

func newRelatedState() *relatedState {
	return &relatedState{active: make(map[string]bool), rows: make(map[string]map[string]json.RawMessage), choices: make(map[string]map[int64]schema.Choice)}
}

func (r *Repository[T]) decodeModel(ctx context.Context, model *schema.Model, row map[string]json.RawMessage, dst any, state *relatedState) error {
	resolveRelation := func(fieldID, id int64, relatedDst any) error {
		parentSchema, ok := r.cache.Get(model.EntityID)
		if !ok {
			return fmt.Errorf("orm: relation field %d has no supported metadata", fieldID)
		}
		var targetEntityID int64
		if fieldID == -1 {
			targetEntityID = 1
		} else if fieldID == 0 {
			targetEntityID = parentSchema.Entity.ParentID()
			if targetEntityID <= 0 {
				return fmt.Errorf("orm: entity %d has no parent entity", model.EntityID)
			}
		} else {
			meta, ok := parentSchema.Fields[fieldID]
			if !ok {
				return fmt.Errorf("orm: relation field %d has no supported metadata", fieldID)
			}
			var err error
			targetEntityID, err = relationTargetID(meta)
			if err != nil {
				return fmt.Errorf("orm: relation field %d: %w", fieldID, err)
			}
		}
		prototype := reflect.ValueOf(relatedDst).Elem().Interface()
		relatedModel, ok := r.cache.ModelFor(prototype)
		if !ok {
			return fmt.Errorf("orm: related model for field %d is not registered", fieldID)
		}
		if relatedModel.EntityID != targetEntityID {
			return fmt.Errorf("orm: field %d requires a model registered for entity %d", fieldID, targetEntityID)
		}
		visitKey := fmt.Sprintf("%d:%d", relatedModel.EntityID, id)
		if state.active[visitKey] {
			return fmt.Errorf("orm: relation cycle at entity %d item %d", relatedModel.EntityID, id)
		}
		state.active[visitKey] = true
		defer delete(state.active, visitKey)
		match, cached := state.rows[visitKey]
		if !cached {
			resp, err := r.client.Do(ctx, api.Request{Action: "select", EntityID: relatedModel.EntityID,
				Params: map[string]any{"filters": map[string]any{"id": id}}})
			if err != nil {
				return err
			}
			var rows []map[string]json.RawMessage
			if err := json.Unmarshal(resp.Data, &rows); err != nil || rows == nil {
				return fmt.Errorf("orm: invalid related select rows: %v", err)
			}
			for _, candidate := range rows {
				var wireID string
				if err := json.Unmarshal(candidate["id"], &wireID); err != nil {
					wireID = string(candidate["id"])
				}
				if wireID == strconv.FormatInt(id, 10) {
					if match != nil {
						return fmt.Errorf("orm: related item %d appears more than once", id)
					}
					match = candidate
				}
			}
			if match == nil {
				return fmt.Errorf("orm: related item %d absent from select response", id)
			}
			state.rows[visitKey] = match
		}
		return r.decodeModel(ctx, relatedModel, match, relatedDst, state)
	}
	return model.DecodeWithLookups(row, dst, schema.LookupResolvers{
		Relation: resolveRelation,
		Choice: func(fieldID, id int64) (schema.Choice, error) {
			key := fmt.Sprintf("%d:%d", model.EntityID, fieldID)
			choices, ok := state.choices[key]
			if !ok {
				loaded, err := r.cache.Choices(ctx, model.EntityID, fieldID)
				if err != nil {
					return schema.Choice{}, err
				}
				choices = make(map[int64]schema.Choice, len(loaded))
				for _, choice := range loaded {
					choices[choice.ID] = choice
				}
				state.choices[key] = choices
			}
			choice, ok := choices[id]
			if !ok {
				return schema.Choice{ID: id}, nil
			}
			choice.Raw = append(json.RawMessage(nil), choice.Raw...)
			return choice, nil
		},
		ItemsByQuery: func(fieldID int64, destination any) error {
			s, ok := r.cache.Get(model.EntityID)
			if !ok {
				return fmt.Errorf("orm: entity %d schema is not cached", model.EntityID)
			}
			meta, ok := s.Fields[fieldID]
			if !ok || meta.Type != "fieldtype_items_by_query" {
				return fmt.Errorf("orm: field %d is not items_by_query", fieldID)
			}
			targetEntityID, err := relationTargetID(meta)
			if err != nil {
				return err
			}
			sourceID, err := wireID(row["id"])
			if err != nil {
				return fmt.Errorf("orm: invalid source item ID: %w", err)
			}
			ids, err := r.itemsByQueryIDs(ctx, model.EntityID, sourceID, meta, targetEntityID)
			if err != nil {
				return err
			}
			list := reflect.ValueOf(destination).Elem()
			elementType := list.Type().Elem()
			for _, id := range ids {
				if id <= 0 {
					return fmt.Errorf("orm: items_by_query resolver returned invalid ID")
				}
				if elementType.Kind() == reflect.Int64 {
					list.Set(reflect.Append(list, reflect.ValueOf(id)))
					continue
				}
				if elementType.Kind() == reflect.String {
					list.Set(reflect.Append(list, reflect.ValueOf(strconv.FormatInt(id, 10))))
					continue
				}
				elemType := elementType
				isPointer := elemType.Kind() == reflect.Pointer
				if isPointer {
					elemType = elemType.Elem()
				}
				target := reflect.New(elemType)
				if err := resolveRelation(fieldID, id, target.Interface()); err != nil {
					return err
				}
				if isPointer {
					list.Set(reflect.Append(list, target))
				} else {
					list.Set(reflect.Append(list, target.Elem()))
				}
			}
			return nil
		},
	})
}

var parentItemsQuery = regexp.MustCompile(`(?i)^\s*e\.parent_item_id\s*=\s*\[id\]\s*$`)

func (r *Repository[T]) itemsByQueryIDs(ctx context.Context, sourceEntityID, sourceItemID int64, field schema.FieldMetadata, targetEntityID int64) ([]int64, error) {
	var wire struct {
		Configuration string `json:"configuration"`
	}
	var config struct {
		WhereQuery string `json:"where_query"`
	}
	if json.Unmarshal(field.Raw, &wire) != nil || json.Unmarshal([]byte(wire.Configuration), &config) != nil {
		return nil, fmt.Errorf("orm: invalid items_by_query configuration")
	}
	if !parentItemsQuery.MatchString(config.WhereQuery) {
		if r.itemsByQueryResolver == nil {
			return nil, fmt.Errorf("orm: field %d items_by_query requires a resolver for its SQL condition", field.ID)
		}
		return r.itemsByQueryResolver(ctx, sourceEntityID, sourceItemID, field, targetEntityID)
	}
	resp, err := r.client.Do(ctx, api.Request{Action: "select", EntityID: targetEntityID, Params: map[string]any{"filters": map[string]any{"parent_item_id": sourceItemID}, "limit": 0}})
	if err != nil {
		return nil, err
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("orm: invalid items_by_query select rows: %v", err)
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		parentID, err := wireID(row["parent_item_id"])
		if err != nil || parentID != sourceItemID {
			return nil, fmt.Errorf("orm: items_by_query select returned an unrelated row")
		}
		id, err := wireID(row["id"])
		if err != nil {
			return nil, fmt.Errorf("orm: invalid items_by_query item ID: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func wireID(raw json.RawMessage) (int64, error) {
	var value string
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
	} else {
		value = string(raw)
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("expected positive decimal ID")
	}
	return id, nil
}

func relationTargetID(meta schema.FieldMetadata) (int64, error) {
	switch meta.Type {
	case "fieldtype_users", "fieldtype_users_approve", "fieldtype_users_ajax":
		return 1, nil
	case "fieldtype_entity_ajax", "fieldtype_entity", "fieldtype_items_by_query":
		var wire struct {
			Configuration string `json:"configuration"`
		}
		if err := json.Unmarshal(meta.Raw, &wire); err != nil {
			return 0, fmt.Errorf("invalid relation metadata: %w", err)
		}
		var config struct {
			EntityID string `json:"entity_id"`
		}
		if err := json.Unmarshal([]byte(wire.Configuration), &config); err != nil {
			return 0, fmt.Errorf("invalid relation configuration: %w", err)
		}
		id, err := strconv.ParseInt(config.EntityID, 10, 64)
		if err != nil || id <= 0 {
			return 0, fmt.Errorf("invalid related entity ID")
		}
		return id, nil
	default:
		return 0, fmt.Errorf("unsupported relation type %q", meta.Type)
	}
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
	page        int
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

// Page selects a positive page number for one server response.
// It was verified on server 3.7; pages beyond the result set may repeat.
func (q *Query[T]) Page(n int) *Query[T] {
	if n <= 0 {
		q.err = fmt.Errorf("orm: page must be positive")
	} else {
		q.page = n
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
	if q.page > 0 {
		params["page"] = q.page
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
	state := newRelatedState()
	for _, row := range rows {
		var item T
		if err := q.repo.decodeModel(ctx, q.repo.model, row, &item, state); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// All is an alias for OneResponse. It returns only one server response.
func (q *Query[T]) All(ctx context.Context) ([]T, error) { return q.OneResponse(ctx) }
