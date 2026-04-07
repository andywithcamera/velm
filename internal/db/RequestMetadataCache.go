package db

import (
	"context"
	"sync"
)

type requestMetadataCacheContextKey struct{}

type requestMetadataCache struct {
	mu sync.RWMutex

	activeApps requestActiveAppsCacheEntry

	yamlTablesByName map[string]requestYAMLTableCacheEntry
	yamlTablesByID   map[string]requestYAMLTableCacheEntry

	physicalBaseTables requestStringListCacheEntry

	tables  map[string]requestTableCacheEntry
	columns map[string]requestColumnsCacheEntry
	views   map[string]requestViewCacheEntry
}

type requestActiveAppsCacheEntry struct {
	loaded bool
	apps   []RegisteredApp
	err    error
}

type requestYAMLTableCacheEntry struct {
	app   RegisteredApp
	table AppDefinitionTable
}

type requestStringListCacheEntry struct {
	loaded bool
	values []string
	err    error
}

type requestTableCacheEntry struct {
	loaded bool
	table  Table
}

type requestColumnsCacheEntry struct {
	loaded  bool
	columns []Column
	err     error
}

type requestViewCacheEntry struct {
	loaded bool
	view   View
}

func WithRequestMetadataCache(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if requestMetadataCacheFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, requestMetadataCacheContextKey{}, newRequestMetadataCache())
}

func requestMetadataCacheFromContext(ctx context.Context) *requestMetadataCache {
	if ctx == nil {
		return nil
	}
	cache, _ := ctx.Value(requestMetadataCacheContextKey{}).(*requestMetadataCache)
	return cache
}

func newRequestMetadataCache() *requestMetadataCache {
	return &requestMetadataCache{
		yamlTablesByName: make(map[string]requestYAMLTableCacheEntry),
		yamlTablesByID:   make(map[string]requestYAMLTableCacheEntry),
		tables:           make(map[string]requestTableCacheEntry),
		columns:          make(map[string]requestColumnsCacheEntry),
		views:            make(map[string]requestViewCacheEntry),
	}
}

func (cache *requestMetadataCache) cachedActiveApps() ([]RegisteredApp, error, bool) {
	if cache == nil {
		return nil, nil, false
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if !cache.activeApps.loaded {
		return nil, nil, false
	}
	return cache.activeApps.apps, cache.activeApps.err, true
}

func (cache *requestMetadataCache) storeActiveApps(apps []RegisteredApp, err error) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.activeApps.loaded {
		return
	}
	cache.activeApps = requestActiveAppsCacheEntry{
		loaded: true,
		apps:   apps,
		err:    err,
	}
	if err != nil {
		return
	}
	for _, app := range apps {
		if app.Definition == nil {
			continue
		}
		for _, table := range app.Definition.Tables {
			if _, exists := cache.yamlTablesByName[table.Name]; !exists {
				cache.yamlTablesByName[table.Name] = requestYAMLTableCacheEntry{
					app:   app,
					table: table,
				}
			}
			cache.yamlTablesByID[yamlTableID(app.Name, table.Name)] = requestYAMLTableCacheEntry{
				app:   app,
				table: table,
			}
		}
	}
}

func (cache *requestMetadataCache) cachedYAMLTableByName(tableName string) (RegisteredApp, AppDefinitionTable, bool, bool) {
	if cache == nil {
		return RegisteredApp{}, AppDefinitionTable{}, false, false
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if !cache.activeApps.loaded {
		return RegisteredApp{}, AppDefinitionTable{}, false, false
	}
	entry, ok := cache.yamlTablesByName[tableName]
	if !ok {
		return RegisteredApp{}, AppDefinitionTable{}, false, true
	}
	return entry.app, entry.table, true, true
}

func (cache *requestMetadataCache) cachedYAMLTableByID(tableID string) (RegisteredApp, AppDefinitionTable, bool, bool) {
	if cache == nil {
		return RegisteredApp{}, AppDefinitionTable{}, false, false
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if !cache.activeApps.loaded {
		return RegisteredApp{}, AppDefinitionTable{}, false, false
	}
	entry, ok := cache.yamlTablesByID[tableID]
	if !ok {
		return RegisteredApp{}, AppDefinitionTable{}, false, true
	}
	return entry.app, entry.table, true, true
}

func (cache *requestMetadataCache) cachedPhysicalBaseTables() ([]string, error, bool) {
	if cache == nil {
		return nil, nil, false
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if !cache.physicalBaseTables.loaded {
		return nil, nil, false
	}
	return cache.physicalBaseTables.values, cache.physicalBaseTables.err, true
}

func (cache *requestMetadataCache) storePhysicalBaseTables(values []string, err error) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.physicalBaseTables.loaded {
		return
	}
	cache.physicalBaseTables = requestStringListCacheEntry{
		loaded: true,
		values: values,
		err:    err,
	}
}

func (cache *requestMetadataCache) cachedTable(tableName string) (Table, bool) {
	if cache == nil {
		return Table{}, false
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	entry, ok := cache.tables[tableName]
	if !ok || !entry.loaded {
		return Table{}, false
	}
	return entry.table, true
}

func (cache *requestMetadataCache) storeTable(tableName string, table Table) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.tables[tableName] = requestTableCacheEntry{
		loaded: true,
		table:  table,
	}
}

func (cache *requestMetadataCache) cachedColumns(tableID string) ([]Column, error, bool) {
	if cache == nil {
		return nil, nil, false
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	entry, ok := cache.columns[tableID]
	if !ok || !entry.loaded {
		return nil, nil, false
	}
	return entry.columns, entry.err, true
}

func (cache *requestMetadataCache) storeColumns(tableID string, columns []Column, err error) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.columns[tableID] = requestColumnsCacheEntry{
		loaded:  true,
		columns: columns,
		err:     err,
	}
}

func (cache *requestMetadataCache) cachedView(tableName string) (View, bool) {
	if cache == nil {
		return View{}, false
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	entry, ok := cache.views[tableName]
	if !ok || !entry.loaded {
		return View{}, false
	}
	return entry.view, true
}

func (cache *requestMetadataCache) storeView(tableName string, view View) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.views[tableName] = requestViewCacheEntry{
		loaded: true,
		view:   view,
	}
}
