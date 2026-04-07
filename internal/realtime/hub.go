package realtime

import (
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	EventTypeRecordChanged  = "record.changed"
	EventTypePresenceUpdate = "presence.updated"
)

type Event struct {
	Type          string         `json:"type"`
	Table         string         `json:"table"`
	RecordID      string         `json:"record_id,omitempty"`
	Kind          string         `json:"kind,omitempty"`
	ActorUserID   string         `json:"actor_user_id,omitempty"`
	ActorClientID string         `json:"actor_client_id,omitempty"`
	At            string         `json:"at"`
	Presence      []PresenceView `json:"presence,omitempty"`
}

type PresenceView struct {
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	Status   string `json:"status"`
}

type subscriber struct {
	table    string
	recordID string
	ch       chan Event
}

type presenceEntry struct {
	ClientID string
	UserID   string
	UserName string
	Status   string
	LastSeen time.Time
}

type Hub struct {
	mu            sync.RWMutex
	now           func() time.Time
	presenceTTL   time.Duration
	subscriptions map[int64]subscriber
	nextSubID     int64
	presenceByKey map[string]map[string]presenceEntry
}

func newHub(now func() time.Time, ttl time.Duration) *Hub {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = 35 * time.Second
	}
	return &Hub{
		now:           now,
		presenceTTL:   ttl,
		subscriptions: map[int64]subscriber{},
		presenceByKey: map[string]map[string]presenceEntry{},
	}
}

var defaultHub = newHub(time.Now, 35*time.Second)

func init() {
	go defaultHub.runJanitor(10 * time.Second)
}

func Subscribe(table, recordID string) (<-chan Event, func()) {
	return defaultHub.Subscribe(table, recordID)
}

func PublishRecordChange(table, recordID, kind, actorUserID, actorClientID string) {
	defaultHub.PublishRecordChange(table, recordID, kind, actorUserID, actorClientID)
}

func UpsertPresence(table, recordID, clientID, userID, userName, status string) {
	defaultHub.UpsertPresence(table, recordID, clientID, userID, userName, status)
}

func PresenceSnapshot(table, recordID string) []PresenceView {
	return defaultHub.PresenceSnapshot(table, recordID)
}

func (h *Hub) Subscribe(table, recordID string) (<-chan Event, func()) {
	table = normalizeTable(table)
	recordID = strings.TrimSpace(recordID)

	ch := make(chan Event, 12)
	h.mu.Lock()
	h.nextSubID++
	subID := h.nextSubID
	h.subscriptions[subID] = subscriber{
		table:    table,
		recordID: recordID,
		ch:       ch,
	}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		delete(h.subscriptions, subID)
		h.mu.Unlock()
	}
	return ch, unsubscribe
}

func (h *Hub) PublishRecordChange(table, recordID, kind, actorUserID, actorClientID string) {
	table = normalizeTable(table)
	recordID = strings.TrimSpace(recordID)
	if table == "" || recordID == "" {
		return
	}
	h.broadcast(Event{
		Type:          EventTypeRecordChanged,
		Table:         table,
		RecordID:      recordID,
		Kind:          strings.TrimSpace(kind),
		ActorUserID:   strings.TrimSpace(actorUserID),
		ActorClientID: strings.TrimSpace(actorClientID),
		At:            h.now().UTC().Format(time.RFC3339Nano),
	})
}

func (h *Hub) UpsertPresence(table, recordID, clientID, userID, userName, status string) {
	table = normalizeTable(table)
	recordID = strings.TrimSpace(recordID)
	clientID = strings.TrimSpace(clientID)
	userID = strings.TrimSpace(userID)
	userName = strings.TrimSpace(userName)
	status = normalizePresenceStatus(status)
	if table == "" || recordID == "" || clientID == "" || userID == "" {
		return
	}

	now := h.now()
	key := presenceKey(table, recordID)

	var event *Event

	h.mu.Lock()
	perRecord := h.presenceByKey[key]
	if perRecord == nil {
		perRecord = map[string]presenceEntry{}
		h.presenceByKey[key] = perRecord
	}
	existing, exists := perRecord[clientID]
	changed := !exists || existing.Status != status || existing.UserName != userName || existing.UserID != userID
	perRecord[clientID] = presenceEntry{
		ClientID: clientID,
		UserID:   userID,
		UserName: userName,
		Status:   status,
		LastSeen: now,
	}
	if changed {
		event = &Event{
			Type:     EventTypePresenceUpdate,
			Table:    table,
			RecordID: recordID,
			At:       now.UTC().Format(time.RFC3339Nano),
			Presence: snapshotFromEntries(perRecord, now, h.presenceTTL),
		}
	}
	h.mu.Unlock()

	if event != nil {
		h.broadcast(*event)
	}
}

func (h *Hub) PresenceSnapshot(table, recordID string) []PresenceView {
	table = normalizeTable(table)
	recordID = strings.TrimSpace(recordID)
	if table == "" || recordID == "" {
		return nil
	}

	key := presenceKey(table, recordID)
	now := h.now()

	h.mu.RLock()
	defer h.mu.RUnlock()
	return snapshotFromEntries(h.presenceByKey[key], now, h.presenceTTL)
}

func (h *Hub) runJanitor(interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		h.cleanupStalePresence()
	}
}

func (h *Hub) cleanupStalePresence() {
	now := h.now()
	events := make([]Event, 0)

	h.mu.Lock()
	for key, perRecord := range h.presenceByKey {
		changed := false
		for clientID, entry := range perRecord {
			if now.Sub(entry.LastSeen) > h.presenceTTL {
				delete(perRecord, clientID)
				changed = true
			}
		}
		if len(perRecord) == 0 {
			delete(h.presenceByKey, key)
		}
		if !changed {
			continue
		}
		table, recordID := splitPresenceKey(key)
		events = append(events, Event{
			Type:     EventTypePresenceUpdate,
			Table:    table,
			RecordID: recordID,
			At:       now.UTC().Format(time.RFC3339Nano),
			Presence: snapshotFromEntries(perRecord, now, h.presenceTTL),
		})
	}
	h.mu.Unlock()

	for _, event := range events {
		h.broadcast(event)
	}
}

func (h *Hub) broadcast(event Event) {
	h.mu.RLock()
	subs := make([]subscriber, 0, len(h.subscriptions))
	for _, sub := range h.subscriptions {
		if sub.table != event.Table {
			continue
		}
		if sub.recordID != "" && sub.recordID != event.RecordID {
			continue
		}
		subs = append(subs, sub)
	}
	h.mu.RUnlock()

	for _, sub := range subs {
		select {
		case sub.ch <- event:
		default:
		}
	}
}

func snapshotFromEntries(entries map[string]presenceEntry, now time.Time, ttl time.Duration) []PresenceView {
	if len(entries) == 0 {
		return nil
	}
	byUser := make(map[string]PresenceView, len(entries))
	for _, entry := range entries {
		if now.Sub(entry.LastSeen) > ttl {
			continue
		}
		name := strings.TrimSpace(entry.UserName)
		if name == "" {
			name = strings.TrimSpace(entry.UserID)
		}
		if name == "" {
			name = "Someone"
		}
		key := strings.TrimSpace(entry.UserID)
		if key == "" {
			key = "name:" + name
		}
		view := PresenceView{
			UserID:   entry.UserID,
			UserName: name,
			Status:   normalizePresenceStatus(entry.Status),
		}
		existing, exists := byUser[key]
		if !exists {
			byUser[key] = view
			continue
		}
		if existing.Status != "editing" && view.Status == "editing" {
			existing.Status = "editing"
		}
		if strings.TrimSpace(existing.UserName) == "" && strings.TrimSpace(view.UserName) != "" {
			existing.UserName = view.UserName
		}
		if strings.TrimSpace(existing.UserID) == "" && strings.TrimSpace(view.UserID) != "" {
			existing.UserID = view.UserID
		}
		byUser[key] = existing
	}
	if len(byUser) == 0 {
		return nil
	}
	views := make([]PresenceView, 0, len(byUser))
	for _, view := range byUser {
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Status != views[j].Status {
			return views[i].Status == "editing"
		}
		if views[i].UserName != views[j].UserName {
			return views[i].UserName < views[j].UserName
		}
		return views[i].UserID < views[j].UserID
	})
	return views
}

func normalizeTable(table string) string {
	return strings.TrimSpace(strings.ToLower(table))
}

func normalizePresenceStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "editing":
		return "editing"
	default:
		return "viewing"
	}
}

func presenceKey(table, recordID string) string {
	return normalizeTable(table) + ":" + strings.TrimSpace(recordID)
}

func splitPresenceKey(key string) (string, string) {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return strings.TrimSpace(key), ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}
