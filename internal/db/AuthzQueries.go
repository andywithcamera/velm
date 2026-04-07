package db

import (
	"context"
	"sync"
	"time"
)

type authzCacheKey struct {
	UserID string
	Action string
	AppID  string
}

type authzCacheEntry struct {
	Allowed   bool
	ExpiresAt time.Time
}

var (
	authzCacheTTL  = 30 * time.Second
	authzCacheMu   sync.RWMutex
	authzCacheData = map[authzCacheKey]authzCacheEntry{}
)

func UserHasGlobalPermission(ctx context.Context, userID, action string) (bool, error) {
	return UserHasPermission(ctx, userID, action, "")
}

func UserHasPermission(ctx context.Context, userID, action, appID string) (bool, error) {
	now := time.Now()
	key := authzCacheKey{UserID: userID, Action: action, AppID: appID}

	authzCacheMu.RLock()
	cached, ok := authzCacheData[key]
	authzCacheMu.RUnlock()
	if ok && now.Before(cached.ExpiresAt) {
		return cached.Allowed, nil
	}

	const query = effectiveUserRolesCTE + `
		SELECT EXISTS (
			SELECT 1
			FROM effective_roles er
			JOIN _role_permission rp ON rp.role_id = er.role_id
			JOIN _permission p ON p._id = rp.permission_id
			WHERE er.user_id = $1
			  AND p.resource = 'platform'
			  AND p.action = $2
			  AND p.scope = 'global'
		)
	`

	var hasPermission bool
	err := Pool.QueryRow(ctx, query, userID, action, appID).Scan(&hasPermission)
	if err != nil {
		return false, err
	}

	authzCacheMu.Lock()
	authzCacheData[key] = authzCacheEntry{
		Allowed:   hasPermission,
		ExpiresAt: now.Add(authzCacheTTL),
	}
	authzCacheMu.Unlock()

	return hasPermission, nil
}

func InvalidateAuthzCache() {
	authzCacheMu.Lock()
	authzCacheData = map[authzCacheKey]authzCacheEntry{}
	authzCacheMu.Unlock()
}

func InvalidateAuthzCacheForUser(userID string) {
	authzCacheMu.Lock()
	defer authzCacheMu.Unlock()
	for key := range authzCacheData {
		if key.UserID == userID {
			delete(authzCacheData, key)
		}
	}
}
