/*
 * One markdown field, everywhere.
 *
 * Every surface that edits markdown renders the "markdown-field" template and
 * gets its behaviour here: a plain textarea for the source, prose rendered by
 * the server for the preview, and [[wikilink]] autocomplete. Before this there
 * were two implementations — one on descriptions (textarea + server preview),
 * one on docs (contenteditable + per-line styling) — which drifted: the docs
 * editor lost blank lines and formatting on save and had no preview at all.
 *
 * Markup contract: [data-markdown-field] wraps
 *   textarea[data-markdown-input]   the source, what submits
 *   [data-markdown-preview]         server-rendered prose
 *   [data-markdown-status]          one-line hint
 *   [data-markdown-error]           shown when the preview request fails
 */
(function () {
  "use strict";

  var PREVIEW_URL = "/api/docs/preview";
  var WIKILINK_URL = "/api/docs/wikilink-search";
  var RENDER_DELAY = 350;
  var SEARCH_DELAY = 150;

  function csrfToken() {
    var meta = document.querySelector('meta[name="csrf-token"]');
    return meta ? meta.content : "";
  }

  function escHtml(s) {
    return String(s == null ? "" : s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;");
  }

  // --- one dropdown, shared by every field on the page ---

  var dropdown = null;
  var searchTarget = null;
  var searchResults = [];
  var searchActive = -1;
  var searchTimer = null;

  function ensureDropdown() {
    if (dropdown) return dropdown;
    dropdown = document.createElement("div");
    dropdown.className = "docs-wikilink-dropdown";
    dropdown.style.display = "none";
    dropdown.style.position = "fixed";
    document.body.appendChild(dropdown);
    return dropdown;
  }

  function hideDropdown() {
    if (!dropdown) return;
    dropdown.style.display = "none";
    dropdown.innerHTML = "";
    searchResults = [];
    searchActive = -1;
    searchTarget = null;
  }

  function positionDropdown(textarea) {
    var box = ensureDropdown();
    var rect = textarea.getBoundingClientRect();
    box.style.left = rect.left + "px";
    box.style.width = Math.min(380, rect.width) + "px";
    if (window.innerHeight - rect.bottom > 160) {
      box.style.top = rect.bottom + 4 + "px";
      box.style.bottom = "auto";
    } else {
      box.style.top = "auto";
      box.style.bottom = window.innerHeight - rect.top + 4 + "px";
    }
  }

  function setActive(idx) {
    searchActive = idx;
    dropdown.querySelectorAll(".docs-wikilink-item").forEach(function (el, i) {
      el.classList.toggle("is-active", i === idx);
    });
  }

  function renderDropdown(textarea, results) {
    var box = ensureDropdown();
    searchResults = results || [];
    searchTarget = textarea;
    searchActive = -1;
    if (!searchResults.length) {
      box.innerHTML = '<div class="docs-wikilink-empty">No matching records found</div>';
    } else {
      box.innerHTML = searchResults
        .map(function (r, i) {
          return (
            '<div class="docs-wikilink-item" data-index="' + i + '">' +
            '<span class="docs-wikilink-item-type">' + escHtml(r.type) + "</span>" +
            "<span>" + escHtml(r.label) + "</span></div>"
          );
        })
        .join("");
      box.querySelectorAll(".docs-wikilink-item").forEach(function (item) {
        item.addEventListener("mousedown", function (e) {
          e.preventDefault();
          var i = parseInt(item.getAttribute("data-index"), 10);
          if (searchResults[i]) insertWikilink(searchResults[i].title);
        });
      });
    }
    positionDropdown(textarea);
    box.style.display = "block";
  }

  function wikilinkContext(textarea) {
    var before = textarea.value.substring(0, textarea.selectionStart);
    var m = before.match(/\[\[([^\]\n]*)$/);
    if (!m) return null;
    return { query: m[1], openAt: before.lastIndexOf("[["), cursor: textarea.selectionStart };
  }

  function insertWikilink(title) {
    var textarea = searchTarget;
    if (!textarea) { hideDropdown(); return; }
    var ctx = wikilinkContext(textarea);
    if (!ctx) { hideDropdown(); return; }
    textarea.value =
      textarea.value.substring(0, ctx.openAt) + "[[" + title + "]]" + textarea.value.substring(ctx.cursor);
    var caret = ctx.openAt + 2 + title.length + 2;
    textarea.setSelectionRange(caret, caret);
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
    hideDropdown();
  }

  function searchWikilinks(textarea, query) {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(function () {
      fetch(WIKILINK_URL + "?q=" + encodeURIComponent(query), {
        credentials: "same-origin",
        headers: { "X-Requested-With": "XMLHttpRequest" },
      })
        .then(function (r) { return r.ok ? r.json() : []; })
        .then(function (results) { renderDropdown(textarea, results); })
        .catch(hideDropdown);
    }, SEARCH_DELAY);
  }

  // --- field behaviour ---

  function expand(input) {
    if (!input.hasAttribute("data-autoexpand")) return;
    // A hidden textarea reports scrollHeight 0, so only measure it when it is
    // laid out; the mode switch re-expands on the way back to source.
    if (!input.offsetParent) return;
    input.style.height = "auto";
    input.style.height = input.scrollHeight + "px";
  }

  function setMode(field, mode) {
    var input = field.querySelector("textarea[data-markdown-input]");
    if (!input) return;
    // A readonly field has no write affordance, so it stays as prose.
    if (field.getAttribute("data-markdown-readonly") === "1") mode = "preview";
    field.setAttribute("data-markdown-mode", mode);
    var status = field.querySelector("[data-markdown-status]");
    if (status) {
      status.textContent = mode === "write" ? "Markdown. Editing." : "Markdown. Click to edit.";
    }
    if (mode === "write") expand(input);
  }

  // Render through the server so the preview matches what is actually stored:
  // the same renderer that produces the read view, with wikilinks resolved.
  function refresh(input) {
    var field = input.closest("[data-markdown-field]");
    if (!field) return;
    var previewId = input.getAttribute("data-markdown-preview-id");
    var preview = previewId ? document.getElementById(previewId) : null;
    if (!preview) return;
    clearTimeout(input._velmMarkdownTimer);
    input._velmMarkdownTimer = setTimeout(function () {
      fetch(PREVIEW_URL, {
        method: "POST",
        body: new URLSearchParams({ markdown_body: input.value || "" }),
        credentials: "same-origin",
        headers: { "X-CSRF-Token": csrfToken() },
      })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (data) {
          if (!data) return;
          preview.innerHTML = data.html || "";
          input.dispatchEvent(new Event("markdown-preview-ok", { bubbles: true }));
        })
        .catch(function () {
          // A failed render must not look like an empty document: the reader
          // would believe the field had nothing in it.
          preview.textContent = input.value || "";
          input.dispatchEvent(new Event("markdown-preview-failed", { bubbles: true }));
        });
    }, RENDER_DELAY);
  }

  function initField(field) {
    var input = field.querySelector("textarea[data-markdown-input]");
    if (!input) return;
    var readonly = field.getAttribute("data-markdown-readonly") === "1";
    var error = field.querySelector("[data-markdown-error]");
    var empty = (input.value || "").trim() === "";

    var preferred = field.getAttribute("data-markdown-default-mode") || "";
    if (preferred === "preview" || preferred === "write") {
      setMode(field, preferred);
    } else {
      // Reading is the common case (open a task, read the brief); editing is
      // the occasional one. An empty field still opens in source, because a
      // preview of nothing is not a starting point.
      setMode(field, empty ? "write" : "preview");
    }

    var preview = field.querySelector("[data-markdown-preview]");
    if (preview && !readonly) {
      var beginEditing = function (event) {
        if (event && event.target.closest("a")) return;
        setMode(field, "write");
        input.focus();
        // Put the caret at the end rather than the start of the text.
        if (event && event.type === "click") {
          var end = input.value.length;
          try { input.setSelectionRange(end, end); } catch (_) {}
        }
      };
      preview.addEventListener("click", beginEditing);
      preview.addEventListener("focus", beginEditing);
    }

    input.addEventListener("focus", function () { setMode(field, "write"); });
    input.addEventListener("blur", function () {
      // Moving focus to another control in the same field is not leaving it.
      window.setTimeout(function () {
        if (field.contains(document.activeElement)) return;
        refresh(input);
        setMode(field, (input.value || "").trim() === "" ? "write" : "preview");
      }, 0);
    });

    if (error) {
      input.addEventListener("markdown-preview-failed", function () { error.classList.remove("hidden"); });
      input.addEventListener("markdown-preview-ok", function () { error.classList.add("hidden"); });
    }

    input.addEventListener("input", function () { refresh(input); expand(input); });

    // [[wikilink]] autocomplete
    input.addEventListener("input", function () {
      var ctx = wikilinkContext(input);
      if (ctx) searchWikilinks(input, ctx.query);
      else if (searchTarget === input) hideDropdown();
    });
    input.addEventListener("keydown", function (e) {
      if (!dropdown || dropdown.style.display === "none" || searchTarget !== input) return;
      var items = dropdown.querySelectorAll(".docs-wikilink-item");
      if (e.key === "Escape") { e.preventDefault(); hideDropdown(); }
      else if (e.key === "ArrowDown") { e.preventDefault(); setActive(Math.min(searchActive + 1, items.length - 1)); }
      else if (e.key === "ArrowUp") { e.preventDefault(); setActive(Math.max(searchActive - 1, 0)); }
      else if ((e.key === "Enter" || e.key === "Tab") && searchActive >= 0 && searchResults[searchActive]) {
        e.preventDefault();
        insertWikilink(searchResults[searchActive].title);
      }
    });
    input.addEventListener("blur", function () { setTimeout(hideDropdown, 150); });

    expand(input);
    // Render the stored text once on load: the preview starts empty so the
    // page can paint before the request completes.
    if ((input.value || "").trim() !== "") refresh(input);
  }

  function init() {
    document.querySelectorAll("[data-markdown-field]").forEach(initField);
  }

  // Width changes reflow the text, so a height measured before the change is
  // wrong. Cheaper than a ResizeObserver for a handful of fields.
  var resizeTimer = null;
  window.addEventListener("resize", function () {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function () {
      document.querySelectorAll("textarea[data-markdown-input]").forEach(expand);
    }, 120);
  });

  window.addEventListener("scroll", function () {
    if (dropdown && dropdown.style.display !== "none" && searchTarget) {
      positionDropdown(searchTarget);
    }
  }, { passive: true });

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }

  // Exposed so a page that inserts a markdown field after load can initialise
  // it (htmx swaps, dynamic sections).
  window.velmMarkdownField = { init: init, refresh: refresh };
})();
