// One place decides whether a URL is safe to hand to window.location.
// Assigning javascript:, data: or vbscript: URLs executes them, so anything
// that is not http(s) is refused. Relative URLs resolve against the current page
// and keep their own scheme, so they pass.
(function () {
  "use strict";

  function safeNavigationHref(rawHref) {
    var href = String(rawHref == null ? "" : rawHref).trim();
    if (href === "") return "";
    try {
      var url = new URL(href, window.location.href);
      if (url.protocol !== "http:" && url.protocol !== "https:") return "";
      return url.href;
    } catch (error) {
      return "";
    }
  }

  window.velmSafeNavigationHref = safeNavigationHref;
})();
