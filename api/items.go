package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// InsertResult contains the ID returned for a single inserted item.
// Insert accepts a JSON number or a decimal JSON string for this ID.
type InsertResult struct {
	ID int64 `json:"id"`
}

// SelectParams contains select parameters. Page was observed on server 3.7.
type SelectParams struct {
	Limit        *int
	SelectFields []int64
	Filters      map[string]any
	RowsPerPage  *int
	Page         *int
}

// Insert creates one item and returns its ID.
func (c *Client) Insert(
	ctx context.Context,
	entityID int64,
	item map[string]any,
) (InsertResult, error) {
	resp, err := c.Do(ctx, Request{
		Action:   "insert",
		EntityID: entityID,
		Params: map[string]any{
			"items": item,
		},
	})
	if err != nil {
		return InsertResult{}, err
	}

	var wire struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(resp.Data, &wire); err != nil {
		return InsertResult{}, fmt.Errorf(
			"api: decode insert result: %w", err,
		)
	}
	if len(wire.ID) == 0 || string(wire.ID) == "null" {
		return InsertResult{}, fmt.Errorf("api: insert result has no ID")
	}
	idText := string(wire.ID)
	if wire.ID[0] == '"' {
		if err := json.Unmarshal(wire.ID, &idText); err != nil {
			return InsertResult{}, fmt.Errorf("api: decode insert ID: %w", err)
		}
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		return InsertResult{}, fmt.Errorf("api: parse insert ID: %w", err)
	}
	return InsertResult{ID: id}, nil
}

// Select returns item fields under their original API response keys.
func (c *Client) Select(
	ctx context.Context,
	entityID int64,
	opts SelectParams,
) ([]map[string]any, error) {
	params := make(map[string]any)

	if opts.Limit != nil {
		params["limit"] = *opts.Limit
	}
	if len(opts.SelectFields) > 0 {
		fields := make([]string, len(opts.SelectFields))
		for i, id := range opts.SelectFields {
			fields[i] = strconv.FormatInt(id, 10)
		}
		params["select_fields"] = strings.Join(fields, ",")
	}
	if len(opts.Filters) > 0 {
		params["filters"] = opts.Filters
	}
	if opts.RowsPerPage != nil {
		params["rows_per_page"] = *opts.RowsPerPage
	}
	if opts.Page != nil {
		if *opts.Page < 1 {
			return nil, fmt.Errorf("api: select page must be positive")
		}
		params["page"] = *opts.Page
	}

	resp, err := c.Do(ctx, Request{
		Action:   "select",
		EntityID: entityID,
		Params:   params,
	})
	if err != nil {
		return nil, err
	}

	var items []map[string]any
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		return nil, fmt.Errorf("api: select result is not an array")
	}
	if err := json.Unmarshal(resp.Data, &items); err != nil {
		return nil, fmt.Errorf(
			"api: decode select result: %w", err,
		)
	}

	return items, nil
}

// Update changes every item matching the required criteria.
func (c *Client) Update(
	ctx context.Context,
	entityID int64,
	data map[string]any,
	updateByField map[string]any,
) error {
	if len(data) == 0 {
		return fmt.Errorf("api: update data is empty")
	}
	if !hasCriteria(updateByField) {
		return fmt.Errorf("api: update criteria is empty")
	}

	_, err := c.Do(ctx, Request{
		Action:   "update",
		EntityID: entityID,
		Params: map[string]any{
			"data":            data,
			"update_by_field": updateByField,
		},
	})
	return err
}

// Delete removes every item matching the required criteria.
func (c *Client) Delete(
	ctx context.Context,
	entityID int64,
	deleteByField map[string]any,
) error {
	if !hasCriteria(deleteByField) {
		return fmt.Errorf("api: delete criteria is empty")
	}

	_, err := c.Do(ctx, Request{
		Action:   "delete",
		EntityID: entityID,
		Params: map[string]any{
			"delete_by_field": deleteByField,
		},
	})
	return err
}

func hasCriteria(criteria map[string]any) bool {
	if len(criteria) == 0 {
		return false
	}
	values, err := encodeForm(criteria)
	if err != nil {
		return false
	}
	for _, entries := range values {
		for _, value := range entries {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
	}
	return false
}
