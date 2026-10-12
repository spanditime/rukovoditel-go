package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
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
	index    int
	id       int64
	system   string
	raw      bool
	relation bool
	choice   bool
	query    bool
	idOnly   bool
	dateTime bool
	many     bool
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
// Tags are rukovoditel:"<field ID>", a supported system key, or
// rukovoditel:"-,raw" for a map[string]json.RawMessage holding unbound keys.
// Bound values use json.RawMessage, *json.RawMessage, or a read-only string.
// Supported relation fields may bind a nested model, one ID (string or int64),
// or a slice of models or IDs. The system parent_item_id and created_by may
// bind one ID or model. Date fields may bind time.Time or *time.Time.
// An item ID may use int64, string, or json.RawMessage.
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
			if f.Type != reflect.TypeFor[int64]() && f.Type != reflect.TypeFor[string]() && f.Type != reflect.TypeFor[json.RawMessage]() && f.Type != reflect.TypeFor[*json.RawMessage]() {
				return nil, fmt.Errorf("schema: unsupported ID field %s", f.Name)
			}
			mf.system = "id"
		case "date_added", "date_updated":
			mf.system = tag
			if timeFieldType(f.Type) {
				mf.dateTime = true
			} else if f.Type != reflect.TypeFor[string]() && f.Type != reflect.TypeFor[json.RawMessage]() && f.Type != reflect.TypeFor[*json.RawMessage]() {
				return nil, fmt.Errorf("schema: unsupported %s field %s", tag, f.Name)
			}
		case "created_by":
			mf.system = tag
			switch {
			case relatedStructType(f.Type) != nil && f.Type.Kind() != reflect.Slice:
				mf.relation = true
				mf.id = -1
			case idOnlyType(f.Type) && f.Type.Kind() != reflect.Slice:
				mf.idOnly = true
			case f.Type == reflect.TypeFor[json.RawMessage]() || f.Type == reflect.TypeFor[*json.RawMessage]():
			default:
				return nil, fmt.Errorf("schema: unsupported created_by field %s", f.Name)
			}
		case "parent_item_id":
			if s.Entity.ParentID() == 0 {
				return nil, fmt.Errorf("schema: entity %d has no parent entity", s.Entity.ID)
			}
			if f.Type.Kind() == reflect.Slice && relatedStructType(f.Type) != nil {
				return nil, fmt.Errorf("schema: parent_item_id is a single relation")
			}
			mf.system = "parent_item_id"
			if relatedStructType(f.Type) != nil {
				mf.relation = true
			} else if idOnlyType(f.Type) && f.Type.Kind() != reflect.Slice {
				mf.idOnly = true
			} else if f.Type != reflect.TypeFor[int64]() && f.Type != reflect.TypeFor[string]() && f.Type != reflect.TypeFor[json.RawMessage]() && f.Type != reflect.TypeFor[*json.RawMessage]() {
				return nil, fmt.Errorf("schema: unsupported parent item field %s", f.Name)
			}
		default:
			id, err := strconv.ParseInt(tag, 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != tag {
				return nil, fmt.Errorf("schema: invalid field tag %q", tag)
			}
			meta, ok := s.Fields[id]
			if !ok {
				return nil, fmt.Errorf("schema: field %d absent from entity %d", id, s.Entity.ID)
			}
			if meta.Type == "fieldtype_dropdown" && choiceType(f.Type) {
				mf.choice = true
				mf.many = f.Type.Kind() == reflect.Slice
			} else if meta.Type == "fieldtype_items_by_query" && f.Type.Kind() == reflect.Slice && (relatedStructType(f.Type) != nil || idOnlyType(f.Type)) {
				mf.query = true
				mf.many = true
			} else if dateFieldType(meta.Type) && timeFieldType(f.Type) {
				mf.dateTime = true
			} else if relatedStructType(f.Type) != nil {
				if !supportedRelationType(meta.Type) {
					return nil, fmt.Errorf("schema: field %s nested model requires a supported relation field", f.Name)
				}
				mf.relation = true
				mf.many = f.Type.Kind() == reflect.Slice
			} else if supportedRelationType(meta.Type) && idOnlyType(f.Type) {
				mf.idOnly = true
				mf.many = f.Type.Kind() == reflect.Slice
			} else if f.Type != reflect.TypeFor[json.RawMessage]() && f.Type != reflect.TypeFor[*json.RawMessage]() && f.Type != reflect.TypeFor[string]() {
				return nil, fmt.Errorf("schema: field %s must use json.RawMessage, *json.RawMessage, string, or a supported related struct", f.Name)
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

func relatedStructType(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct && t != reflect.TypeFor[json.RawMessage]() && t != reflect.TypeFor[time.Time]() && t != reflect.TypeFor[Choice]() {
		return t
	}
	return nil
}

func choiceType(t reflect.Type) bool {
	if t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t == reflect.TypeFor[Choice]()
}

func timeFieldType(t reflect.Type) bool {
	return t == reflect.TypeFor[time.Time]() || t == reflect.TypeFor[*time.Time]()
}

func dateFieldType(t string) bool {
	switch t {
	case "fieldtype_input_date_extra", "fieldtype_input_datetime", "fieldtype_date_added", "fieldtype_date_updated", "fieldtype_user_last_login_date":
		return true
	}
	return false
}

func supportedRelationType(t string) bool {
	switch t {
	case "fieldtype_users", "fieldtype_users_approve", "fieldtype_users_ajax", "fieldtype_entity", "fieldtype_entity_ajax":
		return true
	}
	return false
}

func idOnlyType(t reflect.Type) bool {
	if t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	return t == reflect.TypeFor[string]() || t == reflect.TypeFor[int64]()
}

// Decode reads one select row. When <ID>_db_value exists for a bound field,
// it takes precedence over <ID>. Nested related structs require
// DecodeWithRelations; their source keys remain in the raw map. Unbound keys
// are retained when the model declares a rukovoditel:"-,raw" field.
func (m *Model) Decode(row map[string]json.RawMessage, dst any) error {
	for _, f := range m.fields {
		if f.relation || f.choice || f.query {
			return fmt.Errorf("schema: relation %s requires DecodeWithRelations", relationKey(f))
		}
	}
	return m.decodeBase(row, dst)
}

func (m *Model) decodeBase(row map[string]json.RawMessage, dst any) error {
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
		if f.relation || f.idOnly || f.choice || f.query {
			continue
		}
		key := f.system
		if key == "" {
			key = strconv.FormatInt(f.id, 10)
		}
		if f.system == "" {
			if _, ok := row[key+"_db_value"]; ok {
				key += "_db_value"
			}
		}
		data, ok := row[key]
		if !ok {
			continue
		}
		field := v.Field(f.index)
		if f.dateTime {
			parsed, present, err := parseDateTime(data)
			if err != nil {
				return fmt.Errorf("schema: field %s date/time: %w", relationKey(f), err)
			}
			field.Set(reflect.Zero(field.Type()))
			if present {
				if field.Kind() == reflect.Pointer {
					field.Set(reflect.ValueOf(&parsed))
				} else {
					field.Set(reflect.ValueOf(parsed))
				}
			}
		} else if f.system != "" && field.Kind() == reflect.Int64 {
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
		} else if field.Kind() == reflect.String {
			if bytes.Equal(data, []byte("null")) {
				return fmt.Errorf("schema: field %s is null; use json.RawMessage to preserve null", v.Type().Field(f.index).Name)
			}
			var value string
			if err := json.Unmarshal(data, &value); err != nil || len(data) == 0 || data[0] != '"' {
				return fmt.Errorf("schema: field %s requires a JSON string", v.Type().Field(f.index).Name)
			}
			field.SetString(value)
		} else if field.Kind() == reflect.Pointer {
			copy := json.RawMessage(bytes.Clone(data))
			field.Set(reflect.ValueOf(&copy))
		} else {
			field.Set(reflect.ValueOf(json.RawMessage(bytes.Clone(data))))
		}
		delete(rest, key)
	}
	for _, f := range m.fields {
		if !f.idOnly {
			continue
		}
		field := v.Field(f.index)
		field.Set(reflect.Zero(field.Type()))
		raw, ok, err := relationRaw(row, f)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ids, err := relationIDs(raw)
		if err != nil {
			return fmt.Errorf("schema: relation %s ID: %w", relationKey(f), err)
		}
		if !f.many && len(ids) > 1 {
			return fmt.Errorf("schema: relation %s has multiple IDs; use a slice binding", relationKey(f))
		}
		for _, id := range ids {
			value := reflect.ValueOf(id)
			if f.many {
				if field.Type().Elem().Kind() == reflect.String {
					value = reflect.ValueOf(strconv.FormatInt(id, 10))
				}
				field.Set(reflect.Append(field, value))
			} else if field.Kind() == reflect.String {
				field.SetString(strconv.FormatInt(id, 10))
			} else {
				field.SetInt(id)
			}
		}
		key := relationKey(f)
		if f.system == "" {
			key += "_db_value"
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

// DecodeWithRelations decodes a row and resolves supported relation
// bindings through resolve. The callback receives a pointer to the nested
// model. fieldID is -1 for created_by and 0 for parent_item_id. Missing
// and empty relation values leave the nested field zero.
func (m *Model) DecodeWithRelations(row map[string]json.RawMessage, dst any, resolve func(fieldID, id int64, dst any) error) error {
	return m.DecodeWithLookups(row, dst, LookupResolvers{Relation: resolve})
}

// LookupResolvers supplies values that require metadata or additional reads.
// ItemsByQuery receives a pointer to the bound slice. Relation receives -1
// for created_by and 0 for parent_item_id.
type LookupResolvers struct {
	Relation     func(fieldID, id int64, dst any) error
	Choice       func(fieldID, id int64) (Choice, error)
	ItemsByQuery func(fieldID int64, dst any) error
}

// DecodeWithLookups decodes a row, resolving nested records, dropdown choices,
// and items_by_query slices through the supplied callbacks.
func (m *Model) DecodeWithLookups(row map[string]json.RawMessage, dst any, lookups LookupResolvers) error {
	if err := m.decodeBase(row, dst); err != nil {
		return err
	}
	v := reflect.ValueOf(dst).Elem()
	for _, f := range m.fields {
		if !f.relation && !f.choice && !f.query {
			continue
		}
		field := v.Field(f.index)
		field.Set(reflect.Zero(field.Type()))
		if f.query {
			if lookups.ItemsByQuery == nil {
				return fmt.Errorf("schema: field %d items_by_query resolver is required", f.id)
			}
			if err := lookups.ItemsByQuery(f.id, field.Addr().Interface()); err != nil {
				return fmt.Errorf("schema: resolve field %d items_by_query: %w", f.id, err)
			}
			continue
		}
		raw, ok, err := relationRaw(row, f)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ids, err := relationIDs(raw)
		if err != nil {
			return fmt.Errorf("schema: relation %s ID: %w", relationKey(f), err)
		}
		if len(ids) == 0 {
			continue
		}
		if !f.many && len(ids) != 1 {
			return fmt.Errorf("schema: relation %s has multiple IDs; use a slice binding", relationKey(f))
		}
		if f.choice && lookups.Choice == nil {
			return fmt.Errorf("schema: field %d choice resolver is required", f.id)
		}
		if f.relation && lookups.Relation == nil {
			return fmt.Errorf("schema: relation %s resolver is required", relationKey(f))
		}
		targetType := field.Type()
		if f.many {
			targetType = targetType.Elem()
		}
		pointerElement := targetType.Kind() == reflect.Pointer
		if targetType.Kind() == reflect.Pointer {
			targetType = targetType.Elem()
		}
		for _, id := range ids {
			target := reflect.New(targetType)
			if f.choice {
				choice, err := lookups.Choice(f.id, id)
				if err != nil {
					return fmt.Errorf("schema: resolve field %d choice: %w", f.id, err)
				}
				target.Elem().Set(reflect.ValueOf(choice))
			} else if err := lookups.Relation(f.id, id, target.Interface()); err != nil {
				return fmt.Errorf("schema: resolve relation %s: %w", relationKey(f), err)
			}
			if f.many {
				if pointerElement {
					field.Set(reflect.Append(field, target))
				} else {
					field.Set(reflect.Append(field, target.Elem()))
				}
			} else if pointerElement {
				field.Set(target)
			} else {
				field.Set(target.Elem())
			}
		}
	}
	return nil
}

func relationKey(f modelField) string {
	if f.system != "" {
		return f.system
	}
	return strconv.FormatInt(f.id, 10)
}

func relationRaw(row map[string]json.RawMessage, f modelField) (json.RawMessage, bool, error) {
	key := relationKey(f)
	if f.system == "" {
		key += "_db_value"
	}
	raw, ok := row[key]
	if !ok && f.system == "" {
		if base := row[relationKey(f)]; len(base) > 0 && !bytes.Equal(base, []byte(`""`)) && !bytes.Equal(base, []byte("null")) {
			return nil, false, fmt.Errorf("schema: field %d relation has no _db_value", f.id)
		}
	}
	return raw, ok, nil
}

func relationIDs(raw json.RawMessage) ([]int64, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte(`""`)) {
		return nil, nil
	}
	text := string(raw)
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
	}
	parts := strings.Split(text, ",")
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id < 0 {
			return nil, fmt.Errorf("expected positive decimal IDs separated by commas")
		}
		if id == 0 && len(parts) == 1 {
			return nil, nil
		}
		if id == 0 {
			return nil, fmt.Errorf("zero in multiple relation IDs")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func parseDateTime(raw json.RawMessage) (time.Time, bool, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte(`""`)) {
		return time.Time{}, false, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return time.Time{}, false, fmt.Errorf("expected JSON string: %w", err)
	}
	for _, layout := range []string{"02.01.2006 15:04", "02.01.2006", "01/02/2006 15:04"} {
		if parsed, err := time.ParseInLocation(layout, text, time.UTC); err == nil {
			return parsed, true, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("unsupported date/time format")
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
		if f.raw {
			continue
		}
		if f.system != "" {
			if f.system != "id" && !v.Field(f.index).IsZero() {
				return nil, fmt.Errorf("schema: %s is read-only in typed writes", f.system)
			}
			continue
		}
		if f.dateTime {
			if !v.Field(f.index).IsZero() {
				return nil, fmt.Errorf("schema: field %d date/time has no confirmed write codec", f.id)
			}
			continue
		}
		if f.relation || f.idOnly || f.choice || f.query {
			if !v.Field(f.index).IsZero() {
				return nil, fmt.Errorf("schema: field %d relation is read-only in typed writes", f.id)
			}
			continue
		}
		meta, ok := s.Fields[f.id]
		if !ok {
			return nil, fmt.Errorf("schema: field %d absent from current schema", f.id)
		}
		field := v.Field(f.index)
		if field.Kind() == reflect.String {
			return nil, fmt.Errorf("schema: field %d string binding is read-only; use json.RawMessage for writes", f.id)
		}
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
			if meta.Type == "fieldtype_formula" {
				return nil, fmt.Errorf("schema: field %d formula is read-only", f.id)
			}
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
