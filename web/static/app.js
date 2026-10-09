// lantern — small client-side helpers. htmx handles the rest.
(function () {
  "use strict";

  var THEME_KEY = "lantern-theme";
  var root = document.documentElement;

  function currentTheme() {
    return root.getAttribute("data-theme") === "light" ? "light" : "dark";
  }

  function syncToggle(btn) {
    var next = currentTheme() === "dark" ? "light" : "dark";
    btn.setAttribute("aria-label", "Switch to " + next + " theme");
    btn.setAttribute("title", "Switch to " + next + " theme");
  }

  function setTheme(theme) {
    root.setAttribute("data-theme", theme);
    try { localStorage.setItem(THEME_KEY, theme); } catch (e) { /* storage blocked */ }
  }

  document.addEventListener("DOMContentLoaded", function () {
    document.querySelectorAll("[data-theme-toggle]").forEach(function (btn) {
      syncToggle(btn);
      btn.addEventListener("click", function () {
        setTheme(currentTheme() === "dark" ? "light" : "dark");
        syncToggle(btn);
      });
    });
  });

  // Keep several open tabs in the same theme.
  window.addEventListener("storage", function (e) {
    if (e.key === THEME_KEY && (e.newValue === "light" || e.newValue === "dark")) {
      root.setAttribute("data-theme", e.newValue);
      document.querySelectorAll("[data-theme-toggle]").forEach(syncToggle);
    }
  });
})();
