package api

import (
	"context"
	"encoding/json"
)

// GetEntities returns the raw data for all available entities.
func (c *Client) GetEntities(ctx context.Context) (json.RawMessage, error) {
	return c.rawAction(ctx, "get_entities", 0, nil)
}

// GetEntityFields returns raw field metadata for an entity.
func (c *Client) GetEntityFields(ctx context.Context, entityID int64) (json.RawMessage, error) {
	return c.rawAction(ctx, "get_entity_fields", entityID, nil)
}

// GetFieldChoices returns raw choices for a field of an entity.
func (c *Client) GetFieldChoices(ctx context.Context, entityID, fieldID int64) (json.RawMessage, error) {
	return c.rawAction(ctx, "get_field_choices", entityID, map[string]any{"field_id": fieldID})
}

// GetProcessButtons returns raw process button data for an entity.
// A nil itemID omits the optional item_id parameter.
func (c *Client) GetProcessButtons(ctx context.Context, entityID int64, itemID *int64) (json.RawMessage, error) {
	var params map[string]any
	if itemID != nil {
		params = map[string]any{"item_id": *itemID}
	}
	return c.rawAction(ctx, "get_process_buttons", entityID, params)
}

// RunProcess runs a process for an item and returns its raw response data.
func (c *Client) RunProcess(ctx context.Context, entityID, itemID, processID int64) (json.RawMessage, error) {
	return c.rawAction(ctx, "run_process", entityID, map[string]any{
		"item_id":    itemID,
		"process_id": processID,
	})
}

func (c *Client) rawAction(ctx context.Context, action string, entityID int64, params map[string]any) (json.RawMessage, error) {
	resp, err := c.Do(ctx, Request{Action: action, EntityID: entityID, Params: params})
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}
