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

  // ---------- Ask AI page ----------

  // Reads a text/event-stream body and calls onEvent(name, data) per event.
  function readEvents(body, onEvent) {
    var reader = body.getReader();
    var decoder = new TextDecoder();
    var buffer = "";
    function handle(chunk) {
      var name = "message", data = "";
      chunk.split("\n").forEach(function (line) {
        if (line.indexOf("event:") === 0) name = line.slice(6).trim();
        else if (line.indexOf("data:") === 0) data += line.slice(5).trim();
      });
      if (data) {
        try { onEvent(name, JSON.parse(data)); } catch (e) { /* ignore malformed event */ }
      }
    }
    function pump() {
      return reader.read().then(function (r) {
        if (r.done) {
          if (buffer.trim()) handle(buffer);
          return;
        }
        buffer += decoder.decode(r.value, { stream: true });
        var parts = buffer.split("\n\n");
        buffer = parts.pop();
        parts.forEach(handle);
        return pump();
      });
    }
    return pump();
  }

  function el(tag, className, text) {
    var e = document.createElement(tag);
    if (className) e.className = className;
    if (text != null) e.textContent = text;
    return e;
  }

  function initAsk() {
    var form = document.querySelector("[data-ask-form]");
    if (!form) return;
    var submit = form.querySelector("[data-ask-submit]");
    var stop = form.querySelector("[data-ask-stop]");
    var note = form.querySelector("[data-ask-note]");
    var noteText = note.textContent;
    var question = form.querySelector("textarea[name=question]");
    var answerBox = document.querySelector("[data-answer]");
    var sourcesBox = document.querySelector("[data-sources]");
    var running = null; // AbortController while a question runs

    function provider() { return form.querySelector("input[name=provider]:checked"); }

    function sync() {
      var p = provider();
      var ok = !!p && p.getAttribute("data-configured") === "true";
      submit.disabled = !ok || !!running;
      stop.hidden = !running;
      note.classList.toggle("is-warning", !!p && !ok);
      note.textContent = p && !ok
        ? p.getAttribute("data-name") + " has no API key. Set " + p.getAttribute("data-key-var") + " and restart lantern."
        : noteText;
    }

    function showError(message) {
      answerBox.replaceChildren();
      var banner = el("div", "banner banner-danger");
      banner.setAttribute("role", "alert");
      banner.appendChild(el("div", null, message));
      answerBox.appendChild(banner);
    }

    function showProgress() {
      var list = el("ol", "ask-progress");
      answerBox.replaceChildren(list);
      sourcesBox.replaceChildren(el("p", "muted", "Collecting sources…"));
      return list;
    }

    function addStep(list, message) {
      var prev = list.querySelector(".is-current");
      if (prev) prev.classList.remove("is-current");
      list.appendChild(el("li", "is-current", message));
    }

    function showAnswer(res) {
      var body = el("div", "prose answer-body");
      // Rendered server-side by goldmark in safe mode: text is escaped, raw
      // HTML and script URLs are dropped.
      body.innerHTML = res.answer_html;
      answerBox.replaceChildren(body, el("p", "answer-meta",
        "Answered by " + res.provider + " (" + res.model + ") in " + res.elapsed + "."));

      sourcesBox.replaceChildren();
      if (!res.sources.length) {
        sourcesBox.appendChild(el("p", "muted", "No documents were used."));
        return;
      }
      var list = el("ul", "source-list");
      res.sources.forEach(function (s) {
        var a = el("a");
        a.href = s.url;
        a.appendChild(el("span", "source-title", s.title || s.path));
        a.appendChild(el("span", "source-path", s.collection + "/" + s.path));
        var li = el("li");
        li.appendChild(a);
        list.appendChild(li);
      });
      sourcesBox.appendChild(list);
    }

    function finish() {
      running = null;
      sync();
    }

    form.addEventListener("change", sync);
    question.addEventListener("keydown", function (e) {
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        form.requestSubmit();
      }
    });
    stop.addEventListener("click", function () {
      if (running) running.abort();
    });

    form.addEventListener("submit", function (e) {
      e.preventDefault();
      if (running || submit.disabled) return;
      var text = question.value.trim();
      if (!text) { question.focus(); return; }
      var collections = Array.prototype.map.call(
        form.querySelectorAll("input[name=c]:checked"), function (c) { return c.value; });

      running = new AbortController();
      sync();
      var steps = showProgress();
      addStep(steps, "Starting…");
      var answered = false;

      fetch("/api/ask", {
        method: "POST",
        headers: { "Content-Type": "application/json", "Accept": "text/event-stream" },
        body: JSON.stringify({ provider: provider().value, question: text, collections: collections }),
        signal: running.signal
      }).then(function (res) {
        var type = res.headers.get("Content-Type") || "";
        if (!res.ok || type.indexOf("text/event-stream") !== 0) {
          return res.json().then(function (j) { showError(j.error || "Request failed."); },
                                 function () { showError("Request failed (HTTP " + res.status + ")."); });
        }
        return readEvents(res.body, function (name, data) {
          if (name === "progress") addStep(steps, data.message);
          else if (name === "answer") { answered = true; showAnswer(data); }
          else if (name === "error") { answered = true; showError(data.message); }
        }).then(function () {
          if (!answered) showError("The connection closed before an answer arrived.");
        });
      }).catch(function (err) {
        if (err.name === "AbortError") showError("Stopped.");
        else showError("Could not reach lantern: " + err.message);
      }).then(finish);
    });

    sync();
  }

  document.addEventListener("DOMContentLoaded", function () {
    initAsk();
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
