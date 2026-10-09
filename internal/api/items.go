package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type InsertResult struct {
	ID int64 `json:"id"`
}

type SelectParams struct {
	Limit        *int
	SelectFields []int64
	ReportsID    *int64
	Filters      map[string]any
	RowsPerPage  *int
	Page         *int
}

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

	var result InsertResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return InsertResult{}, fmt.Errorf(
			"api: decode insert result: %w", err,
		)
	}

	return result, nil
}

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
	if opts.ReportsID != nil {
		params["reports_id"] = *opts.ReportsID
	}
	if len(opts.Filters) > 0 {
		params["filters"] = opts.Filters
	}
	if opts.RowsPerPage != nil {
		params["rows_per_page"] = *opts.RowsPerPage
	}
	if opts.Page != nil {
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
	if err := json.Unmarshal(resp.Data, &items); err != nil {
		return nil, fmt.Errorf(
			"api: decode select result: %w", err,
		)
	}

	return items, nil
}

func (c *Client) Update(
	ctx context.Context,
	entityID int64,
	data map[string]any,
	updateByField map[string]any,
) error {
	if len(data) == 0 {
		return fmt.Errorf("api: update data is empty")
	}
	if len(updateByField) == 0 {
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

func (c *Client) Delete(
	ctx context.Context,
	entityID int64,
	deleteByField map[string]any,
) error {
	if len(deleteByField) == 0 {
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
