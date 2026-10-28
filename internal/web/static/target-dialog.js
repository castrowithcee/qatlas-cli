// Target chooser of the connection setup page. It only enhances the plain Targets field: without this script
// the page works as before. All text from the provider is set through textContent/value, never as markup.
"use strict";
(function () {
  var root = document.getElementById("target-dialog");
  if (!root) {
    return;
  }
  var form = root.closest("form");
  var targetsInput = document.getElementById("targets");
  // The exclusions field exists only for a provider that declares exclusions.
  var excludeInput = root.dataset.excludes === "true" ? document.getElementById("exclude") : null;
  if (!form || !targetsInput) {
    return;
  }
  var kinds = [];
  try {
    kinds = JSON.parse(root.dataset.kinds || "[]");
  } catch (e) {
    return;
  }
  if (!kinds.length) {
    return;
  }
  // A provider whose kinds are all unlisted has no list to choose from: the panel then only resolves a web
  // address.
  var hasList = kinds.some(function (k) { return !k.unlisted; });
  var csrf = root.dataset.csrf || "";
  var listURL = root.dataset.listUrl;
  var resolveURL = root.dataset.resolveUrl;
  var multiple = root.dataset.multiple === "true";
  var noun = root.dataset.noun || "target";
  var draftFields = ["provider", "service", "svcname", "svcbaseurl", "credential", "credname", "credstorage", "targets"];
  var maxLabelChars = 200;
  var searchDelay = 300;

  var levels = [];
  var requestSeq = 0;
  var searchTimer = null;
  var marks = new Map();

  function el(tag, props, text) {
    var node = document.createElement(tag);
    if (props) {
      Object.keys(props).forEach(function (k) {
        node.setAttribute(k, props[k]);
      });
    }
    if (text !== undefined) {
      node.textContent = text;
    }
    return node;
  }

  function button(text, onClick) {
    var b = el("button", { type: "button" }, text);
    b.addEventListener("click", onClick);
    return b;
  }

  // The panel. None of its inputs has a name, so none is submitted with the form.
  var openButton = button(hasList ? "Choose " + noun + " from the provider" : "Add " + noun + " from a web address", function () {
    panel.hidden = !panel.hidden;
    if (hasList && !panel.hidden && !levels.length) {
      startTop();
    }
  });
  var panel = el("div", { class: "target-panel" });
  panel.hidden = true;
  var urlInput = el("input", { type: "text", id: "target-url", autocomplete: "off" });
  var urlLabel = el("label", { for: "target-url" }, "Or paste a web address of the service");
  var urlButton = button("Look up", function () {
    resolveFromURL();
  });
  var pathLine = el("p", { class: "hint" });
  var backButton = button("Back", function () {
    if (levels.length > 1) {
      levels.pop();
      marks.clear();
      searchInput.value = "";
      render();
      if (!top().fixed) {
        reload(top());
      }
    }
  });
  var kindSelect = el("select", { id: "target-kind", "aria-label": "Kind" });
  kindSelect.addEventListener("change", function () {
    var lv = top();
    lv.kind = kindSelect.value;
    reload(lv);
  });
  var searchInput = el("input", { type: "search", id: "target-search", autocomplete: "off", "aria-label": "Search" });
  searchInput.addEventListener("input", onSearch);
  var message = el("p", { role: "alert" });
  var table = el("table");
  var head = el("thead");
  var headRow = el("tr");
  ["", "Name", "Kind", "ID", "Path", ""].forEach(function (h) {
    headRow.appendChild(el("th", { scope: "col" }, h));
  });
  head.appendChild(headRow);
  var body = el("tbody");
  table.appendChild(head);
  table.appendChild(body);
  var wrap = el("div", { class: "table-wrap" });
  wrap.hidden = !hasList;
  wrap.appendChild(table);
  var moreButton = button("Load more", function () {
    loadMore();
  });
  var takeButton = button("Add selected", function () {
    take(Array.from(marks.values()));
  });
  var takeExcludeButton = button("Add selected as exclusions", function () {
    take(Array.from(marks.values()), true);
  });
  takeExcludeButton.hidden = !excludeInput;
  var closeButton = button("Close", function () {
    panel.hidden = true;
  });
  var actions = el("p", { class: "actions" });
  [moreButton, takeButton, takeExcludeButton, closeButton].forEach(function (b) {
    actions.appendChild(b);
  });
  [urlLabel, urlInput, urlButton, backButton, pathLine, kindSelect, searchInput, message, wrap, actions].forEach(function (n) {
    panel.appendChild(n);
  });
  root.appendChild(openButton);
  root.appendChild(panel);
  root.hidden = false;

  function top() {
    return levels[levels.length - 1];
  }

  function startTop() {
    var names = kinds.filter(function (k) { return (!(k.parents && k.parents.length) || k.top) && !k.unlisted; }).map(function (k) { return k.name; });
    levels = [{ title: "", kinds: names, kind: names[0], parent: "", entries: [], next: "", serverSearch: false, fixed: false }];
    marks.clear();
    render();
    reload(top());
  }

  function kindInfo(name) {
    return kinds.find(function (k) { return k.name === name; });
  }

  // draft collects only the non-secret fields the server needs to rebuild the connection in setup.
  function draft() {
    var p = new URLSearchParams();
    Array.prototype.forEach.call(form.elements, function (f) {
      if (!f.name || (f.type === "radio" && !f.checked)) {
        return;
      }
      if (draftFields.indexOf(f.name) >= 0 || f.name.indexOf("envname_") === 0) {
        p.append(f.name, f.value);
      }
    });
    p.append("csrf", csrf);
    return p;
  }

  function post(path, params) {
    return fetch(path, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: params.toString()
    }).then(function (res) {
      var type = res.headers.get("Content-Type") || "";
      if (type.indexOf("application/json") !== 0) {
        throw { code: "session", message: "The session expired or the request was refused. Reload the page." };
      }
      return res.json().then(function (data) {
        if (!res.ok) {
          throw data && data.error ? data.error : { code: "runtime", message: "The targets could not be read." };
        }
        return data;
      });
    }, function () {
      throw { code: "network", message: "The request failed. Reload the page." };
    });
  }

  function showError(err) {
    message.replaceChildren();
    message.appendChild(document.createTextNode((err.code || "error") + ": " + (err.message || "")));
    if (err.code === "admin-required") {
      message.appendChild(document.createTextNode(" Approve this browser with the vault passphrase on "));
      var a = el("a", { href: "/" }, "the overview page");
      a.target = "_blank";
      a.rel = "noopener";
      message.appendChild(a);
      message.appendChild(document.createTextNode(" or at the terminal, then try again."));
    } else if (err.code === "unsaved-secret") {
      message.appendChild(document.createTextNode(
        " Save the credential first and set up the connection with it, or type the " + noun + " in the field below."));
    }
  }

  function listParams(lv, cursor) {
    var p = draft();
    p.append("kind", lv.kind);
    p.append("parent", lv.parent);
    p.append("query", lv.serverSearch ? searchInput.value : "");
    p.append("cursor", cursor);
    return p;
  }

  function reload(lv) {
    lv.entries = [];
    lv.next = "";
    marks.clear();
    fetchPage(lv, "");
  }

  function loadMore() {
    var lv = top();
    if (lv && lv.next) {
      fetchPage(lv, lv.next);
    }
  }

  function fetchPage(lv, cursor) {
    var seq = ++requestSeq;
    message.textContent = "Loading...";
    post(listURL, listParams(lv, cursor)).then(function (data) {
      if (seq !== requestSeq || lv !== top()) {
        return;
      }
      message.textContent = "";
      lv.entries = lv.entries.concat(data.entries || []);
      lv.next = data.next || "";
      lv.serverSearch = !!data.server_search;
      render();
    }, function (err) {
      if (seq === requestSeq) {
        showError(err);
      }
    });
  }

  function onSearch() {
    var lv = top();
    if (!lv) {
      return;
    }
    if (lv.serverSearch && !lv.fixed) {
      clearTimeout(searchTimer);
      searchTimer = setTimeout(function () { reload(lv); }, searchDelay);
      return;
    }
    render();
  }

  function resolveFromURL() {
    var value = urlInput.value.trim();
    if (!value) {
      return;
    }
    var p = draft();
    p.append("url", value);
    var seq = ++requestSeq;
    message.textContent = "Loading...";
    post(resolveURL, p).then(function (data) {
      if (seq !== requestSeq) {
        return;
      }
      message.textContent = (data.entries || []).length ? "" : "The address matches nothing.";
      marks.clear();
      var found = { title: "Address", kinds: [], kind: "", parent: "", entries: data.entries || [], next: "", serverSearch: false, fixed: true };
      if (hasList) {
        levels.push(found);
      } else {
        levels = [found];
      }
      render();
    }, function (err) {
      if (seq === requestSeq) {
        showError(err);
      }
    });
  }

  function visible(lv) {
    var q = searchInput.value.trim().toLowerCase();
    if (!q || lv.serverSearch) {
      return lv.entries;
    }
    return lv.entries.filter(function (e) {
      return [e.name, e.target, e.path].some(function (s) { return (s || "").toLowerCase().indexOf(q) >= 0; });
    });
  }

  function open(entry) {
    var children = kinds.filter(function (k) {
      return !k.unlisted && ((k.parents || []).indexOf(entry.kind) >= 0 || (k.nested && k.name === entry.kind));
    }).map(function (k) { return k.name; });
    if (!children.length) {
      return;
    }
    marks.clear();
    searchInput.value = "";
    var lv = { title: entry.name, kinds: children, kind: children[0], parent: entry.target, entries: [], next: "", serverSearch: false, fixed: false };
    levels.push(lv);
    render();
    reload(lv);
  }

  function render() {
    var lv = top();
    wrap.hidden = false;
    pathLine.textContent = levels.map(function (l) { return l.title; }).filter(Boolean).join(" / ");
    backButton.hidden = levels.length < 2;
    kindSelect.replaceChildren();
    lv.kinds.forEach(function (name) {
      var o = el("option", { value: name }, name);
      if (name === lv.kind) {
        o.selected = true;
      }
      kindSelect.appendChild(o);
    });
    kindSelect.hidden = lv.kinds.length < 2;
    searchInput.hidden = lv.fixed;
    moreButton.hidden = !lv.next;
    takeButton.hidden = !multiple;
    takeExcludeButton.hidden = !excludeInput;
    body.replaceChildren();
    visible(lv).forEach(function (e) {
      var row = el("tr");
      var pick = el("td");
      if (multiple || (excludeInput && e.excludable)) {
        var box = el("input", { type: "checkbox", "aria-label": "Select " + e.name });
        box.checked = marks.has(e.target);
        box.addEventListener("change", function () {
          if (box.checked) {
            marks.set(e.target, e);
          } else {
            marks.delete(e.target);
          }
        });
        pick.appendChild(box);
      }
      row.appendChild(pick);
      [e.name, e.kind, e.target, e.path || ""].forEach(function (text) {
        row.appendChild(el("td", null, text));
      });
      var act = el("td");
      act.appendChild(button("Add", function () { take([e]); }));
      if (excludeInput && e.excludable) {
        act.appendChild(button("Add as exclusion", function () { take([e], true); }));
      }
      if (e.has_children) {
        act.appendChild(button("Open", function () { open(e); }));
      }
      row.appendChild(act);
      body.appendChild(row);
    });
  }

  function cleanLabel(name) {
    var s = String(name).replace(/[\p{Cc}\p{Cf}]/gu, " ").split(/\s+/).filter(Boolean).join(" ");
    return Array.from(s).slice(0, maxLabelChars).join("").trim();
  }

  function listed(input) {
    return input.value.split(",").map(function (t) { return t.trim(); }).filter(Boolean);
  }

  function labelField(target) {
    var found = null;
    Array.prototype.forEach.call(form.elements, function (f) {
      if (f.name === "label_" + target) {
        found = f;
      }
    });
    return found;
  }

  // ensureLabel gives the target a label field when the page has none and fills it with the provider's name
  // only while it is empty; the field stays editable.
  function ensureLabel(target, name) {
    var label = cleanLabel(name);
    var field = labelField(target);
    if (!field) {
      var id = "label-picked-" + (form.querySelectorAll(".picked-label").length + 1);
      var wrapper = el("div", { class: "field picked-label" });
      wrapper.appendChild(el("label", { for: id }, "Label for " + target));
      field = el("input", { id: id, type: "text", name: "label_" + target, autocomplete: "off" });
      wrapper.appendChild(field);
      var anchor = targetsInput.closest(".field");
      var last = form.querySelectorAll(".picked-label");
      (last.length ? last[last.length - 1] : anchor).after(wrapper);
    }
    if (label && !field.value) {
      field.value = label;
    }
  }

  // take adds the entries to the targets, or, with asExclusion, to the exclusions. An entry moves out of the
  // other list, so it is never both. The server's checks decide what is valid; the script only offers the
  // exclusion where the server marked the entry excludable.
  function take(entries, asExclusion) {
    var into = asExclusion ? excludeInput : targetsInput;
    var other = asExclusion ? targetsInput : excludeInput;
    if (asExclusion) {
      entries = entries.filter(function (e) { return e.excludable; });
    }
    if (!entries.length || !into) {
      return;
    }
    if (!multiple && !asExclusion) {
      entries = entries.slice(0, 1);
      into.value = "";
    }
    var current = listed(into);
    var moved = other ? listed(other) : [];
    entries.forEach(function (e) {
      if (current.indexOf(e.target) < 0) {
        current.push(e.target);
      }
      moved = moved.filter(function (t) { return t !== e.target; });
      ensureLabel(e.target, e.name);
    });
    into.value = current.join(", ");
    if (other) {
      other.value = moved.join(", ");
    }
    marks.clear();
    panel.hidden = true;
    message.textContent = "";
  }
})();
