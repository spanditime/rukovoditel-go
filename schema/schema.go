// Package schema loads and caches observed Rukovoditel metadata by entity ID.
package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/spanditime/rukovoditel-go/api"
)

// EntityMetadata contains observed entity properties and its full raw JSON object.
// Path lists ancestor IDs from root to immediate parent. SortOrder is the
// position among siblings; Raw retains the original wire values and other
// properties. Value types can vary between server versions.
type EntityMetadata struct {
	ID        int64
	Name      string
	Notes     string
	SortOrder int64
	Path      []int64
	Raw       json.RawMessage
}

// Level returns the number of ancestors in Path.
func (e EntityMetadata) Level() int { return len(e.Path) }

// ParentID returns the last ancestor ID, or zero for a root entity.
func (e EntityMetadata) ParentID() int64 {
	if len(e.Path) == 0 {
		return 0
	}
	return e.Path[len(e.Path)-1]
}

// FieldMetadata contains observed field properties and its full raw JSON object.
// IsHeading and IsRequired are true for JSON "1", 1, or true; all other
// values are false. Type does not determine the JSON type or writability
// of an item value.
type FieldMetadata struct {
	ID         int64
	Type       string
	Name       string
	ShortName  string
	IsHeading  bool
	IsRequired bool
	Raw        json.RawMessage
}

// Choice is a dropdown option. Raw preserves the metadata response; it is nil
// when an item references an ID absent from get_field_choices.
type Choice struct {
	ID   int64
	Name string
	Raw  json.RawMessage
}

// Choices reads options for a cached dropdown field. It does not cache them;
// callers decide when an up-to-date list is needed.
func (c *SchemaCache) Choices(ctx context.Context, entityID, fieldID int64) ([]Choice, error) {
	s, ok := c.Get(entityID)
	if !ok || s.Fields[fieldID].Type != "fieldtype_dropdown" {
		return nil, fmt.Errorf("schema: dropdown field %d is not cached for entity %d", fieldID, entityID)
	}
	raw, err := c.client.GetFieldChoices(ctx, entityID, fieldID)
	if err != nil {
		return nil, fmt.Errorf("schema: load choices for field %d: %w", fieldID, err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("schema: invalid choices for field %d: %v", fieldID, err)
	}
	choices := make([]Choice, 0, len(rows))
	seen := make(map[int64]bool, len(rows))
	for _, row := range rows {
		var wire struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(row, &wire); err != nil {
			return nil, fmt.Errorf("schema: decode choice for field %d: %w", fieldID, err)
		}
		id, err := strconv.ParseInt(wire.ID, 10, 64)
		if err != nil || id <= 0 || seen[id] {
			return nil, fmt.Errorf("schema: invalid or duplicate choice ID for field %d", fieldID)
		}
		seen[id] = true
		choices = append(choices, Choice{ID: id, Name: wire.Name, Raw: append(json.RawMessage(nil), row...)})
	}
	return choices, nil
}

// ShortNameOrName returns ShortName when it is nonempty, otherwise Name.
// The original values remain available in the ShortName and Name fields.
func (f FieldMetadata) ShortNameOrName() string {
	if f.ShortName != "" {
		return f.ShortName
	}
	return f.Name
}

// Schema is a snapshot of metadata for one entity. Callers may modify returned
// values without changing the cache.
type Schema struct {
	Entity EntityMetadata
	Fields map[int64]FieldMetadata
}

// HeadingField returns the field marked IsHeading with the greatest ID.
// It returns false if no field is marked. The meaning of the server flag
// for item titles has not been verified.
func (s Schema) HeadingField() (FieldMetadata, bool) {
	var selected FieldMetadata
	var selectedID int64
	for id, field := range s.Fields {
		if field.IsHeading && id > selectedID {
			selectedID = id
			selected = field
		}
	}
	if selectedID == 0 {
		return FieldMetadata{}, false
	}
	selected.Raw = append(json.RawMessage(nil), selected.Raw...)
	return selected, true
}

// SchemaCache stores snapshots by explicit entity ID. It has no automatic expiry.
type SchemaCache struct {
	client *api.Client
	logger *slog.Logger
	mu     sync.RWMutex
	items  map[int64]Schema
	models map[reflect.Type]registeredModel
}

// NewSchemaCache creates an empty metadata cache for client.
func NewSchemaCache(client *api.Client) (*SchemaCache, error) {
	if client == nil {
		return nil, fmt.Errorf("schema: schema cache requires a client")
	}
	return &SchemaCache{client: client, logger: client.Logger(), items: make(map[int64]Schema), models: make(map[reflect.Type]registeredModel)}, nil
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
func (c *SchemaCache) Refresh(ctx context.Context, entityID int64) (result Schema, err error) {
	start := time.Now()
	defer func() {
		if c.logger == nil {
			return
		}
		if err != nil {
			c.logger.WarnContext(ctx, "rukovoditel schema refresh failed", "entity_id", entityID, "duration", time.Since(start))
			return
		}
		c.logger.InfoContext(ctx, "rukovoditel schema refreshed", "entity_id", entityID, "field_count", len(result.Fields), "duration", time.Since(start))
	}()
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
func (c *SchemaCache) RefreshAll(ctx context.Context) (err error) {
	start := time.Now()
	loaded := 0
	defer func() {
		if c.logger == nil {
			return
		}
		if err != nil {
			c.logger.WarnContext(ctx, "rukovoditel schema refresh all failed", "duration", time.Since(start))
			return
		}
		c.logger.InfoContext(ctx, "rukovoditel schema refreshed all", "entity_count", loaded, "duration", time.Since(start))
	}()
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
		loaded++
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
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			Notes     string   `json:"notes"`
			SortOrder string   `json:"sort_order"`
			Path      []string `json:"path"`
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
		entity := EntityMetadata{ID: id, Name: row.Name, Notes: row.Notes, Raw: raw}
		if row.SortOrder != "" {
			entity.SortOrder, err = strconv.ParseInt(row.SortOrder, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("schema: invalid sort_order for entity %d: %w", id, err)
			}
		}
		for _, value := range row.Path {
			ancestorID, err := strconv.ParseInt(value, 10, 64)
			if err != nil || ancestorID <= 0 {
				return nil, fmt.Errorf("schema: invalid path for entity %d", id)
			}
			entity.Path = append(entity.Path, ancestorID)
		}
		result[id] = entity
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
			ID         string          `json:"id"`
			Type       string          `json:"type"`
			Name       string          `json:"name"`
			ShortName  string          `json:"short_name"`
			IsHeading  json.RawMessage `json:"is_heading"`
			IsRequired json.RawMessage `json:"is_required"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			return Schema{}, fmt.Errorf("schema: decode field metadata: %w", err)
		}
		if row.ID != key {
			return Schema{}, fmt.Errorf("schema: field metadata ID differs from key")
		}
		fields[id] = FieldMetadata{
			ID: id, Type: row.Type, Name: row.Name, ShortName: row.ShortName,
			IsHeading: metadataFlag(row.IsHeading), IsRequired: metadataFlag(row.IsRequired), Raw: raw,
		}
	}
	return Schema{Entity: entity, Fields: fields}, nil
}

func metadataFlag(raw json.RawMessage) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch v := value.(type) {
	case string:
		return v == "1"
	case float64:
		return v == 1
	case bool:
		return v
	}
	return false
}

func cloneSchema(s Schema) Schema {
	s.Entity.Raw = append(json.RawMessage(nil), s.Entity.Raw...)
	s.Entity.Path = append([]int64(nil), s.Entity.Path...)
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
