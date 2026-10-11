// Package schema loads and caches observed Rukovoditel metadata by entity ID.
package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"sync"

	"github.com/spanditime/rukovoditel-go/api"
)

// EntityMetadata contains observed entity properties and its full raw JSON object.
// Other properties and value types can vary between server versions.
type EntityMetadata struct {
	ID   int64
	Name string
	Raw  json.RawMessage
}

// FieldMetadata contains observed field properties and its full raw JSON object.
// Type does not determine the JSON type or writability of an item value.
type FieldMetadata struct {
	ID   int64
	Type string
	Name string
	Raw  json.RawMessage
}

// Schema is a snapshot of metadata for one entity. Callers may modify returned
// values without changing the cache.
type Schema struct {
	Entity EntityMetadata
	Fields map[int64]FieldMetadata
}

// SchemaCache stores snapshots by explicit entity ID. It has no automatic expiry.
type SchemaCache struct {
	client *api.Client
	mu     sync.RWMutex
	items  map[int64]Schema
	models map[reflect.Type]registeredModel
}

// NewSchemaCache creates an empty metadata cache for client.
func NewSchemaCache(client *api.Client) (*SchemaCache, error) {
	if client == nil {
		return nil, fmt.Errorf("schema: schema cache requires a client")
	}
	return &SchemaCache{client: client, items: make(map[int64]Schema), models: make(map[reflect.Type]registeredModel)}, nil
}

// Get returns a copy of a cached snapshot, if present.
func (c *SchemaCache) Get(entityID int64) (Schema, bool) {
	c.mu.RLock()
	schema, ok := c.items[entityID]
	copy := cloneSchema(schema)
	c.mu.RUnlock()
	return copy, ok
}

// ListEntities reads the entities visible to the configured API user.
// It returns them in ID order without changing the cached schemas.
func (c *SchemaCache) ListEntities(ctx context.Context) ([]EntityMetadata, error) {
	entities, err := c.loadEntities(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]EntityMetadata, 0, len(entities))
	for _, entity := range entities {
		result = append(result, entity)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// Refresh fetches entity and field metadata and atomically replaces one snapshot.
// A failed request or invalid response leaves the previous snapshot intact.
func (c *SchemaCache) Refresh(ctx context.Context, entityID int64) (Schema, error) {
	if entityID <= 0 {
		return Schema{}, fmt.Errorf("schema: entity ID must be positive")
	}
	entities, err := c.loadEntities(ctx)
	if err != nil {
		return Schema{}, err
	}
	entity, found := entities[entityID]
	if !found {
		return Schema{}, fmt.Errorf("schema: entity %d absent from metadata response", entityID)
	}
	schema, err := c.loadSchema(ctx, entity)
	if err != nil {
		return Schema{}, err
	}
	c.mu.Lock()
	if err := c.validateModels(entityID, schema); err != nil {
		c.mu.Unlock()
		return Schema{}, err
	}
	c.items[entityID] = schema
	c.mu.Unlock()
	return cloneSchema(schema), nil
}

// RefreshAll loads field metadata for every entity in get_entities and
// atomically replaces the entire cache. If any request or parse fails, the
// previous cache remains intact. It does not return a server-wide snapshot:
// metadata may change while the requests are in progress.
func (c *SchemaCache) RefreshAll(ctx context.Context) error {
	entities, err := c.loadEntities(ctx)
	if err != nil {
		return err
	}
	next := make(map[int64]Schema, len(entities))
	for _, entity := range entities {
		if err := ctx.Err(); err != nil {
			return err
		}
		schema, err := c.loadSchema(ctx, entity)
		if err != nil {
			return fmt.Errorf("schema: load entity %d: %w", entity.ID, err)
		}
		next[entity.ID] = schema
	}
	c.mu.Lock()
	for _, registered := range c.models {
		schema, ok := next[registered.model.EntityID]
		if !ok {
			c.mu.Unlock()
			return fmt.Errorf("schema: registered entity %d absent from refreshed metadata", registered.model.EntityID)
		}
		if err := validateRegistered(registered, schema); err != nil {
			c.mu.Unlock()
			return err
		}
	}
	c.items = next
	c.mu.Unlock()
	return nil
}

func (c *SchemaCache) loadEntities(ctx context.Context) (map[int64]EntityMetadata, error) {
	entitiesRaw, err := c.client.GetEntities(ctx)
	if err != nil {
		return nil, err
	}
	var entities []json.RawMessage
	if err := json.Unmarshal(entitiesRaw, &entities); err != nil || entities == nil {
		return nil, fmt.Errorf("schema: invalid entities metadata: %v", err)
	}
	result := make(map[int64]EntityMetadata, len(entities))
	for _, raw := range entities {
		var row struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, fmt.Errorf("schema: decode entity metadata: %w", err)
		}
		id, err := strconv.ParseInt(row.ID, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("schema: invalid entity metadata ID")
		}
		if _, exists := result[id]; exists {
			return nil, fmt.Errorf("schema: duplicate entity metadata ID %d", id)
		}
		result[id] = EntityMetadata{ID: id, Name: row.Name, Raw: raw}
	}
	return result, nil
}

func (c *SchemaCache) loadSchema(ctx context.Context, entity EntityMetadata) (Schema, error) {
	fieldsRaw, err := c.client.GetEntityFields(ctx, entity.ID)
	if err != nil {
		return Schema{}, err
	}
	var rows map[string]json.RawMessage
	if err := json.Unmarshal(fieldsRaw, &rows); err != nil || rows == nil {
		return Schema{}, fmt.Errorf("schema: invalid fields metadata: %v", err)
	}
	fields := make(map[int64]FieldMetadata, len(rows))
	for key, raw := range rows {
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil || id <= 0 {
			return Schema{}, fmt.Errorf("schema: invalid field metadata key")
		}
		var row struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			return Schema{}, fmt.Errorf("schema: decode field metadata: %w", err)
		}
		if row.ID != key {
			return Schema{}, fmt.Errorf("schema: field metadata ID differs from key")
		}
		fields[id] = FieldMetadata{ID: id, Type: row.Type, Name: row.Name, Raw: raw}
	}
	return Schema{Entity: entity, Fields: fields}, nil
}

func cloneSchema(s Schema) Schema {
	s.Entity.Raw = append(json.RawMessage(nil), s.Entity.Raw...)
	if s.Fields != nil {
		fields := make(map[int64]FieldMetadata, len(s.Fields))
		for id, field := range s.Fields {
			field.Raw = append(json.RawMessage(nil), field.Raw...)
			fields[id] = field
		}
		s.Fields = fields
	}
	return s
}
