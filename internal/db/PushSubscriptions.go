package db

import (
	"context"
	"log"
	"strings"
	"time"

	"velm/internal/push"
)

// Push subscription storage. Endpoint is the unique key: browsers reuse
// one endpoint per origin+permission, re-subscribing upserts it.

type PushSubscriptionUpsert struct {
	UserID    string
	Endpoint  string
	P256DH    string
	Auth      string
	UserAgent string
}

func UpsertPushSubscription(ctx context.Context, in PushSubscriptionUpsert) error {
	_, err := Pool.Exec(ctx, `
INSERT INTO _user_push_subscription (user_id, endpoint, p256dh, auth, user_agent)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (endpoint) DO UPDATE
SET user_id = EXCLUDED.user_id,
    p256dh = EXCLUDED.p256dh,
    auth = EXCLUDED.auth,
    user_agent = EXCLUDED.user_agent,
    last_success = now()
`, in.UserID, in.Endpoint, in.P256DH, in.Auth, truncateString(in.UserAgent, 300))
	return err
}

func DeletePushSubscription(ctx context.Context, userID, endpoint string) error {
	_, err := Pool.Exec(ctx,
		`DELETE FROM _user_push_subscription WHERE user_id = $1 AND endpoint = $2`,
		userID, endpoint)
	return err
}

func ListPushSubscriptions(ctx context.Context, userID string) ([]push.Subscription, error) {
	rows, err := Pool.Query(ctx,
		`SELECT endpoint, p256dh, auth FROM _user_push_subscription WHERE user_id = $1`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subs []push.Subscription
	for rows.Next() {
		var s push.Subscription
		if err := rows.Scan(&s.Endpoint, &s.P256DH, &s.Auth); err != nil {
			return nil, err
		}
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

func DeleteStalePushSubscriptions(ctx context.Context, endpoints []string) error {
	if len(endpoints) == 0 {
		return nil
	}
	_, err := Pool.Exec(ctx,
		`DELETE FROM _user_push_subscription WHERE endpoint = ANY($1)`, endpoints)
	return err
}

func TouchPushSubscriptions(ctx context.Context, endpoints []string) error {
	if len(endpoints) == 0 {
		return nil
	}
	_, err := Pool.Exec(ctx,
		`UPDATE _user_push_subscription SET last_success = $1 WHERE endpoint = ANY($2)`,
		time.Now(), endpoints)
	return err
}

func truncateString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// DispatchWebPush fans a batch of freshly created notifications out to
// each recipient's browser push subscriptions. Best effort and
// asynchronous: failures are logged, never propagated — in-app
// notifications must not fail because push is misconfigured. Stale
// endpoints (gone / gone-permanent) are removed.
func DispatchWebPush(items []UserNotification) {
	if len(items) == 0 || !push.Enabled() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		byUser := make(map[string][]push.Subscription)
		var stale []string
		for _, item := range items {
			subs, err := ListPushSubscriptions(ctx, item.UserID)
			if err != nil {
				log.Printf("push: list subscriptions for %s failed: %v", item.UserID, err)
				continue
			}
			if len(subs) == 0 {
				continue
			}
			byUser[item.UserID] = subs
			res := push.Send(ctx, subs, item.Title, item.Body, item.Href)
			stale = append(stale, res.Stale...)
		}
		if err := DeleteStalePushSubscriptions(ctx, stale); err != nil {
			log.Printf("push: stale subscription cleanup failed: %v", err)
		}
	}()
}
