// Package rukovoditel provides a connection facade over the API, schema, and ORM packages.
package rukovoditel

import (
	"context"
	"fmt"

	"github.com/spanditime/rukovoditel-go/api"
	"github.com/spanditime/rukovoditel-go/orm"
	"github.com/spanditime/rukovoditel-go/schema"
)

// Connection owns one API client and its explicitly refreshed schema cache.
type Connection struct {
	client *api.Client
	cache  *schema.SchemaCache
}

// New creates a connection from one API configuration without sending a request.
func New(cfg api.Config) (*Connection, error) {
	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("rukovoditel: create client: %w", err)
	}
	cache, err := schema.NewSchemaCache(client)
	if err != nil {
		return nil, fmt.Errorf("rukovoditel: create schema cache: %w", err)
	}
	return &Connection{client: client, cache: cache}, nil
}

// Client returns the shared low-level client for actions outside the typed layer.
func (c *Connection) Client() *api.Client { return c.client }

// Refresh loads one entity's metadata. Incompatible changes leave the cached schema intact.
func (c *Connection) Refresh(ctx context.Context, entityID int64) (schema.Schema, error) {
	return c.cache.Refresh(ctx, entityID)
}

// Register binds a Go struct type to an explicit entity ID in the loaded schema.
func (c *Connection) Register(entityID int64, prototype any) (*schema.Model, error) {
	return c.cache.Register(entityID, prototype)
}

// Repository returns a typed repository for a model registered on this connection.
func Repository[T any](c *Connection) (*orm.Repository[T], error) {
	if c == nil {
		return nil, fmt.Errorf("rukovoditel: connection is nil")
	}
	return orm.NewRepository[T](c.client, c.cache)
}
