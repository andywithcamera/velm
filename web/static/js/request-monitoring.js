(function () {
    const endpoint = "/api/monitor/client";
    const requestMeta = document.querySelector('meta[name="request-id"]');
    const csrfMeta = document.querySelector('meta[name="csrf-token"]');
    const initialRequestID = requestMeta ? String(requestMeta.content || "").trim() : "";
    const csrfToken = csrfMeta ? String(csrfMeta.content || "").trim() : "";
    const htmxRequests = new WeakMap();
    let documentMetricSent = false;

    function roundDuration(value) {
        if (!Number.isFinite(value) || value < 0) {
            return "";
        }
        return String(Math.round(value));
    }

    function sameOrigin(url) {
        try {
            return new URL(url, window.location.href).origin === window.location.origin;
        } catch (error) {
            return false;
        }
    }

    function currentPath() {
        return window.location.pathname + window.location.search;
    }

    function sendMetric(metric) {
        const requestID = String(metric.request_id || "").trim();
        if (!requestID) {
            return;
        }

        const params = new URLSearchParams();
        Object.entries(metric).forEach(([key, value]) => {
            if (value === undefined || value === null || value === "") {
                return;
            }
            if (key === "client_payload") {
                params.set(key, JSON.stringify(value));
                return;
            }
            params.set(key, String(value));
        });
        if (csrfToken) {
            params.set("csrf_token", csrfToken);
        }

        if (navigator.sendBeacon) {
            const blob = new Blob([params.toString()], {
                type: "application/x-www-form-urlencoded;charset=UTF-8",
            });
            if (navigator.sendBeacon(endpoint, blob)) {
                return;
            }
        }

        fetch(endpoint, {
            method: "POST",
            body: params.toString(),
            headers: {
                "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8",
                "X-CSRF-Token": csrfToken,
            },
            credentials: "same-origin",
            keepalive: true,
        }).catch(function () {
            return undefined;
        });
    }

    function sendDocumentMetric() {
        if (documentMetricSent || !initialRequestID) {
            return;
        }
        documentMetricSent = true;

        const navEntry = performance.getEntriesByType("navigation")[0];
        if (!navEntry) {
            sendMetric({
                request_id: initialRequestID,
                method: "GET",
                path: currentPath(),
                request_source: "document",
                client_event_type: "document",
                client_total_ms: "",
                client_processing_ms: "",
                client_render_ms: "",
                client_payload: {
                    entry_missing: true,
                },
            });
            return;
        }

        const responseEnd = Number(navEntry.responseEnd || 0);
        const requestStart = Number(navEntry.requestStart || 0);
        const responseStart = Number(navEntry.responseStart || 0);
        const domContentLoadedEnd = Number(navEntry.domContentLoadedEventEnd || 0);
        const loadEventEnd = Number(navEntry.loadEventEnd || 0);

        sendMetric({
            request_id: initialRequestID,
            method: "GET",
            path: currentPath(),
            request_source: "document",
            client_event_type: "document",
            client_nav_type: navEntry.type || "",
            client_total_ms: roundDuration(navEntry.duration),
            client_network_ms: roundDuration(responseEnd - requestStart),
            client_ttfb_ms: roundDuration(responseStart - requestStart),
            client_transfer_ms: roundDuration(responseEnd - responseStart),
            client_processing_ms: roundDuration(loadEventEnd - responseEnd),
            client_render_ms: roundDuration(domContentLoadedEnd - responseEnd),
            client_dom_content_loaded_ms: roundDuration(domContentLoadedEnd),
            client_load_event_ms: roundDuration(loadEventEnd),
            client_payload: {
                decoded_body_size: Number(navEntry.decodedBodySize || 0),
                transfer_size: Number(navEntry.transferSize || 0),
                redirect_count: Number(navEntry.redirectCount || 0),
                dom_interactive_ms: Math.round(Number(navEntry.domInteractive || 0)),
            },
        });
    }

    window.addEventListener("load", function () {
        window.setTimeout(sendDocumentMetric, 0);
    });

    document.addEventListener("htmx:beforeRequest", function (event) {
        const xhr = event && event.detail ? event.detail.xhr : null;
        if (!xhr) {
            return;
        }
        htmxRequests.set(xhr, {
            startedAt: performance.now(),
            method: String(event.detail.requestConfig?.verb || "GET").toUpperCase(),
            path: String(event.detail.pathInfo?.requestPath || event.detail.pathInfo?.finalRequestPath || currentPath()),
            target: event.detail.target ? event.detail.target.id || event.detail.target.tagName || "" : "",
        });
    });

    document.addEventListener("htmx:afterRequest", function (event) {
        const xhr = event && event.detail ? event.detail.xhr : null;
        if (!xhr) {
            return;
        }
        const request = htmxRequests.get(xhr);
        if (!request) {
            return;
        }
        request.responseAt = performance.now();
        request.requestID = String(xhr.getResponseHeader("X-Request-ID") || "").trim();
        request.status = xhr.status;
        request.failed = !event.detail.successful;
        htmxRequests.set(xhr, request);
    });

    document.addEventListener("htmx:afterSettle", function (event) {
        const xhr = event && event.detail ? event.detail.xhr : null;
        if (!xhr) {
            return;
        }
        const request = htmxRequests.get(xhr);
        if (!request || !request.requestID) {
            return;
        }
        const completedAt = performance.now();
        const responseAt = Number(request.responseAt || completedAt);
        sendMetric({
            request_id: request.requestID,
            method: request.method,
            path: request.path,
            request_source: "htmx",
            client_event_type: "htmx",
            client_total_ms: roundDuration(responseAt - request.startedAt),
            client_processing_ms: roundDuration(completedAt - responseAt),
            client_render_ms: roundDuration(completedAt - responseAt),
            client_settle_ms: roundDuration(completedAt - responseAt),
            client_payload: {
                status: request.status,
                failed: request.failed === true,
                target: request.target,
            },
        });
        htmxRequests.delete(xhr);
    });

    if (typeof window.fetch === "function") {
        const originalFetch = window.fetch.bind(window);
        window.fetch = function monitoredFetch(input, init) {
            const requestURL = typeof input === "string" ? input : input && input.url ? input.url : "";
            if (!requestURL || !sameOrigin(requestURL)) {
                return originalFetch(input, init);
            }

            const absoluteURL = new URL(requestURL, window.location.href);
            if (absoluteURL.pathname === endpoint) {
                return originalFetch(input, init);
            }

            const startedAt = performance.now();
            const method = String((init && init.method) || (input && input.method) || "GET").toUpperCase();

            return originalFetch(input, init).then(function (response) {
                const requestID = String(response.headers.get("X-Request-ID") || "").trim();
                if (requestID) {
                    sendMetric({
                        request_id: requestID,
                        method: method,
                        path: absoluteURL.pathname + absoluteURL.search,
                        request_source: "api",
                        client_event_type: "fetch",
                        client_total_ms: roundDuration(performance.now() - startedAt),
                        client_payload: {
                            status: response.status,
                            ok: response.ok,
                        },
                    });
                }
                return response;
            });
        };
    }
})();
