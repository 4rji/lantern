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

  // ---------- Document page ----------

  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise(function (resolve, reject) {
      var ta = document.createElement("textarea");
      ta.value = text;
      ta.setAttribute("readonly", "");
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand("copy") ? resolve() : reject(); } catch (e) { reject(e); }
      document.body.removeChild(ta);
    });
  }

  function initCopyButtons() {
    document.querySelectorAll("[data-copy]").forEach(function (btn) {
      var label = btn.querySelector("[data-copy-label]") || btn;
      var original = label.textContent;
      btn.addEventListener("click", function () {
        copyText(btn.getAttribute("data-copy")).then(function () {
          label.textContent = "Copied";
        }, function () {
          label.textContent = "Copy failed";
        }).then(function () {
          setTimeout(function () { label.textContent = original; }, 1500);
        });
      });
    });
  }

  function initRawToggle() {
    var btn = document.querySelector("[data-raw-toggle]");
    var rendered = document.querySelector("[data-rendered]");
    var raw = document.querySelector("[data-raw]");
    if (!btn || !rendered || !raw) return;
    btn.addEventListener("click", function () {
      var showRaw = btn.getAttribute("aria-pressed") !== "true";
      btn.setAttribute("aria-pressed", String(showRaw));
      raw.hidden = !showRaw;
      rendered.hidden = showRaw;
    });
  }

  function initHeadingAnchors() {
    document.querySelectorAll(".prose :is(h1, h2, h3, h4, h5, h6)[id]").forEach(function (h) {
      var a = document.createElement("a");
      a.className = "anchor";
      a.href = "#" + h.id;
      a.setAttribute("aria-label", "Link to this section");
      a.textContent = "#";
      h.prepend(a);
    });
  }

  // Highlight the TOC entry for the section being read.
  function initTocTracking() {
    var links = document.querySelectorAll(".toc a[href^='#']");
    if (!links.length || !("IntersectionObserver" in window)) return;
    var byId = {};
    links.forEach(function (a) { byId[decodeURIComponent(a.getAttribute("href").slice(1))] = a; });
    var visible = {};
    var observer = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) { visible[e.target.id] = e.isIntersecting; });
      var current = null;
      document.querySelectorAll(".prose [id]").forEach(function (h) {
        if (!current && visible[h.id] && byId[h.id]) current = h.id;
      });
      if (!current) return;
      links.forEach(function (a) { a.classList.remove("active"); });
      byId[current].classList.add("active");
    }, { rootMargin: "-70px 0px -60% 0px" });
    Object.keys(byId).forEach(function (id) {
      var el = document.getElementById(id);
      if (el) observer.observe(el);
    });
  }

  // On narrow windows the folder tree starts collapsed.
  function initTreePanel() {
    var panel = document.querySelector(".tree-panel");
    if (panel && window.matchMedia("(max-width: 900px)").matches) panel.open = false;
  }

  // ---------- Search page ----------

  // Fast mode searches as you type (debounced); Smart mode only on Enter or
  // the button, because each Smart search runs QMD's models.
  function initSearch() {
    var form = document.querySelector("[data-search-form]");
    if (!form) return;
    var q = form.querySelector("input[name=q]");
    var wrap = document.getElementById("results-wrap");
    var timer = null;

    function isSmart() {
      var m = form.querySelector("input[name=mode]:checked");
      return !!m && m.value === "smart";
    }
    function syncMode() {
      form.classList.toggle("is-smart", isSmart());
      if (wrap) wrap.classList.toggle("is-smart", isSmart());
    }
    function searchNow() {
      clearTimeout(timer);
      if (window.htmx) window.htmx.trigger(form, "search-now");
    }

    q.addEventListener("input", function () {
      if (isSmart()) return;
      clearTimeout(timer);
      timer = setTimeout(searchNow, 300);
    });
    form.addEventListener("change", function (e) {
      if (e.target === q) return;
      syncMode();
      if (!isSmart()) searchNow();
    });
    form.addEventListener("submit", function () { clearTimeout(timer); });
    syncMode();
  }

  document.addEventListener("DOMContentLoaded", function () {
    initSearch();
    initCopyButtons();
    initRawToggle();
    initHeadingAnchors();
    initTocTracking();
    initTreePanel();
  });

  // Keep several open tabs in the same theme.
  window.addEventListener("storage", function (e) {
    if (e.key === THEME_KEY && (e.newValue === "light" || e.newValue === "dark")) {
      root.setAttribute("data-theme", e.newValue);
      document.querySelectorAll("[data-theme-toggle]").forEach(syncToggle);
    }
  });
})();
