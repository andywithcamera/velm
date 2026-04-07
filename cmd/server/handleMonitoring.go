package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"velm/internal/db"
)

func handleClientMonitoringBeacon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid monitoring payload", http.StatusBadRequest)
		return
	}

	requestID := strings.TrimSpace(r.FormValue("request_id"))
	if requestID == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	payload := map[string]any{}
	rawPayload := strings.TrimSpace(r.FormValue("client_payload"))
	if rawPayload != "" {
		_ = json.Unmarshal([]byte(rawPayload), &payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.UpsertRequestMetricClient(ctx, db.RequestMetricClientUpdate{
		RequestID:          requestID,
		Method:             strings.TrimSpace(r.FormValue("method")),
		Path:               strings.TrimSpace(r.FormValue("path")),
		RequestSource:      strings.TrimSpace(r.FormValue("request_source")),
		ClientEventType:    strings.TrimSpace(r.FormValue("client_event_type")),
		ClientNavType:      strings.TrimSpace(r.FormValue("client_nav_type")),
		ClientTotalMS:      parseMetricInt(r.FormValue("client_total_ms")),
		ClientNetworkMS:    parseMetricInt(r.FormValue("client_network_ms")),
		ClientTTFBMS:       parseMetricInt(r.FormValue("client_ttfb_ms")),
		ClientTransferMS:   parseMetricInt(r.FormValue("client_transfer_ms")),
		ClientProcessingMS: parseMetricInt(r.FormValue("client_processing_ms")),
		ClientRenderMS:     parseMetricInt(r.FormValue("client_render_ms")),
		ClientDOMContentMS: parseMetricInt(r.FormValue("client_dom_content_loaded_ms")),
		ClientLoadEventMS:  parseMetricInt(r.FormValue("client_load_event_ms")),
		ClientSettleMS:     parseMetricInt(r.FormValue("client_settle_ms")),
		ClientPayload:      payload,
	}); err != nil {
		log.Printf("request metrics client beacon failed: request_id=%s err=%v", requestID, err)
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleMonitoringView(w http.ResponseWriter, r *http.Request) {
	target := "/t/_request_metric"
	if raw := r.URL.RawQuery; raw != "" {
		target += "?" + raw
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func parseMetricInt(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return -1
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return -1
	}
	return value
}
