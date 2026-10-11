package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Model binds a struct to one explicit entity schema snapshot.
type Model struct {
	EntityID  int64
	modelType reflect.Type
	fields    []modelField
}

// FieldID resolves an exported Go field name to its registered CRM field ID.
// System and raw fields have no CRM field ID.
func (m *Model) FieldID(name string) (int64, bool) {
	f, ok := m.modelType.FieldByName(name)
	if !ok {
		return 0, false
	}
	for _, bound := range m.fields {
		if bound.index == f.Index[0] && bound.id > 0 {
			return bound.id, true
		}
	}
	return 0, false
}

type modelField struct {
	index  int
	id     int64
	system string
	raw    bool
}

type registeredModel struct {
	model      *Model
	fieldTypes map[int64]string
}

// Register associates a Go struct type with an entity ID in this cache.
// The entity must have a loaded schema. A type can be registered only once.
// Subsequent refreshes reject incompatible metadata and keep the old snapshot.
func (c *SchemaCache) Register(entityID int64, prototype any) (*Model, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.items[entityID]
	if !ok {
		return nil, fmt.Errorf("schema: entity %d has no cached schema", entityID)
	}
	m, err := RegisterModel(s, prototype)
	if err != nil {
		return nil, err
	}
	if _, exists := c.models[m.modelType]; exists {
		return nil, fmt.Errorf("schema: model type %s already registered", m.modelType)
	}
	types := make(map[int64]string)
	for _, field := range m.fields {
		if field.id > 0 {
			types[field.id] = s.Fields[field.id].Type
		}
	}
	c.models[m.modelType] = registeredModel{model: m, fieldTypes: types}
	return cloneModel(m), nil
}

// ModelFor returns the binding for a registered Go struct type.
func (c *SchemaCache) ModelFor(prototype any) (*Model, bool) {
	t := reflect.TypeOf(prototype)
	if t == nil {
		return nil, false
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	c.mu.RLock()
	entry, ok := c.models[t]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return cloneModel(entry.model), true
}

func cloneModel(m *Model) *Model {
	copy := *m
	copy.fields = append([]modelField(nil), m.fields...)
	return &copy
}

func (c *SchemaCache) validateModels(entityID int64, s Schema) error {
	for _, entry := range c.models {
		if entry.model.EntityID == entityID {
			if err := validateRegistered(entry, s); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRegistered(entry registeredModel, s Schema) error {
	for id, oldType := range entry.fieldTypes {
		field, ok := s.Fields[id]
		if !ok {
			return fmt.Errorf("schema: registered model %s: field %d removed from entity %d", entry.model.modelType, id, s.Entity.ID)
		}
		if field.Type != oldType {
			return fmt.Errorf("schema: registered model %s: field %d type changed from %q to %q", entry.model.modelType, id, oldType, field.Type)
		}
	}
	return nil
}

// RegisterModel validates a struct prototype against a metadata snapshot.
// Tags are rukovoditel:"<field ID>", rukovoditel:"id", or
// rukovoditel:"-,raw" for a map[string]json.RawMessage holding unbound keys.
// Bound values use json.RawMessage or *json.RawMessage. An ID may use int64.
func RegisterModel(s Schema, prototype any) (*Model, error) {
	t := reflect.TypeOf(prototype)
	if t == nil {
		return nil, fmt.Errorf("schema: nil model")
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || s.Entity.ID <= 0 {
		return nil, fmt.Errorf("schema: model requires a struct and entity schema")
	}
	m := &Model{EntityID: s.Entity.ID, modelType: t}
	seen := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("rukovoditel")
		if tag == "" || tag == "-" {
			continue
		}
		if !f.IsExported() {
			return nil, fmt.Errorf("schema: tagged field %s is unexported", f.Name)
		}
		mf := modelField{index: i}
		key := tag
		switch tag {
		case "-,raw":
			if f.Type != reflect.TypeFor[map[string]json.RawMessage]() {
				return nil, fmt.Errorf("schema: raw field %s must be map[string]json.RawMessage", f.Name)
			}
			mf.raw = true
		case "id":
			if f.Type != reflect.TypeFor[int64]() && f.Type != reflect.TypeFor[json.RawMessage]() && f.Type != reflect.TypeFor[*json.RawMessage]() {
				return nil, fmt.Errorf("schema: unsupported ID field %s", f.Name)
			}
			mf.system = "id"
		default:
			id, err := strconv.ParseInt(tag, 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != tag {
				return nil, fmt.Errorf("schema: invalid field tag %q", tag)
			}
			if _, ok := s.Fields[id]; !ok {
				return nil, fmt.Errorf("schema: field %d absent from entity %d", id, s.Entity.ID)
			}
			if f.Type != reflect.TypeFor[json.RawMessage]() && f.Type != reflect.TypeFor[*json.RawMessage]() {
				return nil, fmt.Errorf("schema: field %s must use json.RawMessage or *json.RawMessage", f.Name)
			}
			mf.id = id
		}
		if seen[key] {
			return nil, fmt.Errorf("schema: duplicate tag %q", key)
		}
		seen[key] = true
		m.fields = append(m.fields, mf)
	}
	if len(m.fields) == 0 {
		return nil, fmt.Errorf("schema: model has no bindings")
	}
	return m, nil
}

// Decode reads one select row without changing JSON value types. Unbound keys
// are retained when the model declares a rukovoditel:"-,raw" field.
func (m *Model) Decode(row map[string]json.RawMessage, dst any) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("schema: decode destination must be a struct pointer")
	}
	v = v.Elem()
	if v.Type() != m.modelType {
		return fmt.Errorf("schema: decode destination type differs from registered model")
	}
	rest := make(map[string]json.RawMessage, len(row))
	for k, val := range row {
		rest[k] = bytes.Clone(val)
	}
	for _, f := range m.fields {
		if f.raw {
			continue
		}
		key := f.system
		if key == "" {
			key = strconv.FormatInt(f.id, 10)
		}
		data, ok := row[key]
		if !ok {
			continue
		}
		field := v.Field(f.index)
		if f.system == "id" && field.Kind() == reflect.Int64 {
			var value string
			if len(data) > 0 && data[0] == '"' {
				if err := json.Unmarshal(data, &value); err != nil {
					return fmt.Errorf("schema: decode id: %w", err)
				}
			} else {
				value = string(data)
			}
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return fmt.Errorf("schema: decode id: %w", err)
			}
			field.SetInt(id)
		} else if field.Kind() == reflect.Pointer {
			copy := json.RawMessage(bytes.Clone(data))
			field.Set(reflect.ValueOf(&copy))
		} else {
			field.Set(reflect.ValueOf(json.RawMessage(bytes.Clone(data))))
		}
		delete(rest, key)
	}
	for _, f := range m.fields {
		if f.raw {
			v.Field(f.index).Set(reflect.ValueOf(rest))
		}
	}
	return nil
}

// EncodeWrite returns form values for insert/update. Only JSON strings bound
// to fieldtype_input are encoded. Missing and nil pointer fields are omitted;
// JSON null, system fields, unknown keys and other field types are rejected.
func (m *Model) EncodeWrite(s Schema, src any) (map[string]any, error) {
	if s.Entity.ID != m.EntityID {
		return nil, fmt.Errorf("schema: entity schema mismatch")
	}
	v := reflect.ValueOf(src)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, fmt.Errorf("schema: nil model")
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return nil, fmt.Errorf("schema: write source must be a struct")
	}
	if v.Type() != m.modelType {
		return nil, fmt.Errorf("schema: write source type differs from registered model")
	}
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("schema: write source must be a struct")
	}
	out := map[string]any{}
	for _, f := range m.fields {
		if f.raw || f.system != "" {
			continue
		}
		meta, ok := s.Fields[f.id]
		if !ok {
			return nil, fmt.Errorf("schema: field %d absent from current schema", f.id)
		}
		field := v.Field(f.index)
		if field.Kind() == reflect.Pointer {
			if field.IsNil() {
				continue
			}
			field = field.Elem()
		}
		data := json.RawMessage(field.Bytes())
		if len(data) == 0 {
			continue
		}
		if meta.Type != "fieldtype_input" {
			return nil, fmt.Errorf("schema: field %d type %s has no write codec", f.id, meta.Type)
		}
		if bytes.Equal(data, []byte("null")) {
			return nil, fmt.Errorf("schema: field %d JSON null has no confirmed write format", f.id)
		}
		var value string
		if err := json.Unmarshal(data, &value); err != nil || !strings.HasPrefix(string(data), `"`) {
			return nil, fmt.Errorf("schema: field %d requires a JSON string", f.id)
		}
		out["field_"+strconv.FormatInt(f.id, 10)] = value
	}
	return out, nil
}
