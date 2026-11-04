// Behaviours that used to live in inline on* attributes. A nonce cannot cover
// inline event handlers, so the markup declares intent with data attributes and
// this file does the wiring.
(function () {
  "use strict";

  function navigate(url) {
    if (!url) return;
    if (typeof window.navigateWithTransition === "function") {
      window.navigateWithTransition(url);
      return;
    }
    window.location.href = url;
  }

  function isModifiedClick(event) {
    return event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey;
  }

  document.addEventListener("click", function (event) {
    var stop = event.target.closest("[data-stop-propagation]");
    if (stop) {
      event.stopPropagation();
    }

    var confirmTarget = event.target.closest("[data-confirm]:not(form)");
    if (confirmTarget) {
      var message = confirmTarget.getAttribute("data-confirm");
      if (message && !window.confirm(message)) {
        event.preventDefault();
        event.stopImmediatePropagation();
        return;
      }
    }

    var action = event.target.closest("[data-action]");
    if (action && !isModifiedClick(event)) {
      var name = action.getAttribute("data-action");
      if (name === "run-adhoc-script" && typeof window.runAdhocScript === "function") {
        event.preventDefault();
        window.runAdhocScript();
        return;
      }
      if (name === "run-script-dry-run" && typeof window.runScriptDryRun === "function") {
        event.preventDefault();
        window.runScriptDryRun();
        return;
      }
    }

    var navigable = event.target.closest("[data-navigate-url], [data-navigate-record]");
    if (navigable && !isModifiedClick(event)) {
      var url = navigable.getAttribute("data-navigate-url") || navigable.dataset.recordUrl;
      if (url) {
        event.preventDefault();
        navigate(url);
      }
    }
  });

  document.addEventListener("submit", function (event) {
    var form = event.target;
    var message = form.getAttribute("data-confirm");
    if (message && !window.confirm(message)) {
      event.preventDefault();
      event.stopImmediatePropagation();
    }
  });

  document.addEventListener("change", function (event) {
    var target = event.target;
    if (target.matches("[data-submit-form]") && target.form) {
      target.form.submit();
    }
  });
})();
