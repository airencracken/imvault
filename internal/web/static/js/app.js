// imvault client-side enhancements: drag-and-drop, clipboard paste and the
// small amount of feedback the server-rendered pages cannot provide.
(function () {
  "use strict";

  var input = document.getElementById("file-input");
  var dropzone = document.getElementById("dropzone");
  var count = document.getElementById("file-count");
  var form = document.getElementById("upload-form");

  var bulkForm = document.getElementById("bulk-tags");
  if (bulkForm) {
    var bulkCount = document.getElementById("bulk-count");
    var bulkSubmit = document.getElementById("bulk-submit");
    var albumSelect = document.getElementById("bulk-album-select");
    var albumCreate = document.getElementById("bulk-album-create");
    var albumTitle = document.getElementById("bulk-album-title");
    var albumError = document.getElementById("bulk-album-error");
    // The organise panel stays out of the way until something is ticked.
    // Without this script it renders collapsed and still works.
    var bulkPanel = document.getElementById("bulk-panel");
    var lastSelected = 0;
    function picks() {
      return Array.prototype.slice.call(document.querySelectorAll('input[name="files"][form="bulk-tags"]'));
    }
    function updateSelection() {
      var selected = picks().filter(function (pick) { return pick.checked; }).length;
      bulkCount.textContent = selected + " selected";
      if (bulkPanel) {
        bulkPanel.hidden = selected === 0;
        if (selected > 0 && lastSelected === 0) bulkPanel.open = true;
      }
      lastSelected = selected;
      bulkSubmit.disabled = selected === 0;
      ["bulk-album", "bulk-visibility"].forEach(function (id) {
        var button = document.getElementById(id);
        if (button) button.disabled = selected === 0;
      });
      if (albumSelect && albumCreate && albumTitle) {
        var creating = albumSelect.value === "new";
        albumCreate.hidden = !creating;
        albumTitle.disabled = !creating;
        if (!creating && albumError) albumError.hidden = true;
      }
    }
    function selectPage(checked) {
      picks().forEach(function (pick) { pick.checked = checked; });
      updateSelection();
    }
    document.getElementById("bulk-selection").hidden = false;
    document.getElementById("bulk-select-all").addEventListener("click", function () { selectPage(true); });
    document.getElementById("bulk-clear").addEventListener("click", function () { selectPage(false); });
    document.addEventListener("change", updateSelection);
    document.body.addEventListener("htmx:afterSwap", updateSelection);
    window.addEventListener("pageshow", updateSelection);
    bulkForm.addEventListener("submit", function (event) {
      updateSelection();
      if (bulkSubmit.disabled) event.preventDefault();
      // Album actions skip the unrelated required tags field. Validate only
      // the new album title, keeping the selected photos and draft on errors.
      if (event.submitter && event.submitter.id === "bulk-album" && albumTitle && !albumTitle.disabled) {
        if (!albumTitle.value.trim()) {
          event.preventDefault();
          if (albumError) albumError.hidden = false;
          albumTitle.focus();
        }
      }
    });
    if (albumTitle) albumTitle.addEventListener("input", function () { if (albumError) albumError.hidden = true; });
    updateSelection();
  }

  if (input && count) {
    input.addEventListener("change", function () {
      render(input.files);
    });
  }

  function render(files) {
    if (!count) return;
    if (!files || files.length === 0) {
      count.textContent = "";
      return;
    }
    var names = [];
    for (var i = 0; i < files.length; i++) names.push(files[i].name);
    count.textContent = files.length + " file" + (files.length === 1 ? "" : "s") + " selected";
    count.title = names.join("\n");
  }

  // accepts reports whether the file input would take a file, by the same
  // accept list the picker uses, so a clip dropped or pasted is treated exactly
  // like one chosen by hand. The server validates properly; this only avoids
  // staging something obviously wrong.
  function accepts(file) {
    var type = (file && file.type) || "";
    if (!type) return false;
    var allowed = (input.getAttribute("accept") || "").split(",");
    for (var i = 0; i < allowed.length; i++) {
      var rule = allowed[i].trim();
      if (!rule) continue;
      if (rule.slice(-2) === "/*" ? type.indexOf(rule.slice(0, -1)) === 0 : type === rule) return true;
    }
    return false;
  }

  // Assigning to input.files requires a DataTransfer, which every browser that
  // supports drag-and-drop provides. It is built on demand rather than at load,
  // so an environment without it still gets the rest of this script. It reports
  // how many files were staged; when none qualify the current selection is left
  // alone rather than replaced with nothing.
  function stage(files) {
    if (typeof DataTransfer === "undefined") return 0;

    var transfer = new DataTransfer();
    for (var i = 0; i < files.length; i++) {
      if (accepts(files[i])) transfer.items.add(files[i]);
    }
    if (transfer.files.length === 0) {
      if (count) count.textContent = "None of those files can be uploaded here.";
      return 0;
    }

    input.files = transfer.files;
    render(input.files);
    return transfer.files.length;
  }

  if (dropzone && input && form) {
    ["dragenter", "dragover"].forEach(function (evt) {
      dropzone.addEventListener(evt, function (e) {
        e.preventDefault();
        dropzone.classList.add("over");
      });
    });

    ["dragleave", "drop"].forEach(function (evt) {
      dropzone.addEventListener(evt, function (e) {
        e.preventDefault();
        dropzone.classList.remove("over");
      });
    });

    dropzone.addEventListener("drop", function (e) {
      if (e.dataTransfer && e.dataTransfer.files.length) {
        stage(e.dataTransfer.files);
      }
    });

    // Clicking anywhere in the dropzone opens the picker.
    dropzone.addEventListener("click", function (e) {
      if (e.target === input) return;
      if (e.target.closest("label")) return;
      input.click();
    });
  }

  // Paste-to-upload, unless the user is typing into a field.
  document.addEventListener("paste", function (e) {
    if (!input || !form) return;
    var target = e.target;
    if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA")) return;

    var items = e.clipboardData && e.clipboardData.items;
    if (!items) return;

    var picked = [];
    for (var i = 0; i < items.length; i++) {
      if (items[i].kind === "file") {
        var file = items[i].getAsFile();
        if (file) picked.push(file);
      }
    }
    if (picked.length === 0) return;

    e.preventDefault();
    if (stage(picked) > 0 && window.htmx) window.htmx.trigger(form, "submit");
  });

  // Clear the selection once an upload completes so the next one starts fresh.
  document.body.addEventListener("htmx:afterRequest", function (e) {
    if (!form || e.detail.elt !== form) return;
    if (e.detail.successful) {
      // Clearing the value empties a file input, with no need for a
      // DataTransfer of its own.
      input.value = "";
      render(null);
    }
  });

  // htmx leaves the page alone on a 5xx, which is right for a crash and wrong
  // for "the server is busy, here is what to say about it". A 503 from an
  // upload carries a fragment meant to be shown.
  document.body.addEventListener("htmx:beforeSwap", function (e) {
    if (!e.detail.xhr || e.detail.xhr.status !== 503) return;
    e.detail.shouldSwap = true;
    e.detail.isError = false;
  });

  // Hovering an animated tile swaps the still thumbnail for the animation, and
  // leaving it swaps back so the grid stays cheap to scroll.
  document.body.addEventListener("mouseover", function (e) {
    var img = e.target.closest ? e.target.closest("img[data-anim]") : null;
    if (!img || img.dataset.playing === "1") return;
    img.dataset.playing = "1";
    img.src = img.dataset.anim;
  });

  document.body.addEventListener("mouseout", function (e) {
    var img = e.target.closest ? e.target.closest("img[data-anim]") : null;
    if (!img || img.dataset.playing !== "1") return;
    // Ignore the transition between the image and its own descendants.
    if (img.contains(e.relatedTarget)) return;
    img.dataset.playing = "0";
    img.src = img.dataset.still;
  });
})();

/* ---------------------------------------------------------------------------
 * Small behaviours declared in the markup with data attributes. Every one of
 * them is an enhancement: the forms and links work without this script.
 * ------------------------------------------------------------------------- */

(function () {
  "use strict";

  // Row filtering for a server-rendered table. Exposed for the tests.
  function matches(row, query) {
    var q = (query || "").trim().toLowerCase();
    return !q || ((row.dataset && row.dataset.search) || "").indexOf(q) !== -1;
  }

  function summary(rows, query) {
    if (!rows) return "";
    if (!(query || "").trim()) return rows.length + " total";
    var shown = rows.filter(function (row) { return matches(row, query); }).length;
    return shown + " of " + rows.length + " shown";
  }

  function byId(selector) {
    return selector && selector.charAt(0) === "#" ? document.getElementById(selector.slice(1)) : null;
  }

  function filterTable(input) {
    var table = byId(input.getAttribute("data-table-filter"));
    if (!table) return;
    var rows = Array.prototype.slice.call(table.querySelectorAll("tr[data-search]"));
    rows.forEach(function (row) { row.hidden = !matches(row, input.value); });
    var label = byId(input.getAttribute("data-table-summary"));
    if (label) label.textContent = summary(rows, input.value);
  }

  // Confirmation for destructive forms, in place of window.confirm. The form
  // or the button that submits it names the question in data-confirm; the
  // dialog is the confirm_dialog partial in the layout. Accepting resubmits
  // the same form with the same button, so htmx sends exactly the request it
  // would have sent.
  var pending = null;

  function dialog() { return document.getElementById("confirm-dialog"); }

  function ask(form, submitter, source) {
    var box = dialog();
    if (!box || typeof box.showModal !== "function") return false;
    pending = { form: form, submitter: submitter };
    document.getElementById("confirm-message").textContent = source.getAttribute("data-confirm");
    var detail = document.getElementById("confirm-detail");
    detail.textContent = source.getAttribute("data-confirm-detail") || "";
    detail.hidden = !detail.textContent;
    box.querySelector("[data-confirm-accept]").textContent = source.getAttribute("data-confirm-action") || "Confirm";
    box.showModal();
    return true;
  }

  function settle(accepted) {
    var box = dialog();
    var current = pending;
    pending = null;
    if (box && box.open) box.close();
    if (!accepted || !current) return;
    current.form.dataset.confirmed = "1";
    if (current.submitter && current.form.requestSubmit) current.form.requestSubmit(current.submitter);
    else if (current.form.requestSubmit) current.form.requestSubmit();
    else current.form.submit();
  }

  function onSubmit(event) {
    var form = event.target;
    if (!form || !form.getAttribute) return;

    // Typing a name to confirm: the server checks it too.
    var expected = form.getAttribute("data-type-to-confirm");
    if (expected !== null) {
      var typed = form.querySelector('input[name="confirm"]');
      if (!typed || typed.value !== expected) {
        event.preventDefault();
        if (typed) typed.focus();
        return;
      }
    }

    if (form.dataset && form.dataset.confirmed === "1") {
      delete form.dataset.confirmed;
      return;
    }
    var submitter = event.submitter || null;
    var source = submitter && submitter.getAttribute && submitter.getAttribute("data-confirm") !== null ? submitter : form;
    if (source.getAttribute("data-confirm") === null) return;
    if (ask(form, submitter, source)) {
      // Stopped in the capture phase, so htmx never sees the first submit.
      event.preventDefault();
      event.stopPropagation();
    }
  }

  function onClick(event) {
    var target = event.target;
    if (!target || !target.closest) return;
    if (target.closest("[data-confirm-accept]")) return settle(true);
    if (target.closest("[data-confirm-cancel]") || target === dialog()) return settle(false);

    var copy = target.closest("[data-copy]");
    if (copy) {
      var field = byId(copy.getAttribute("data-copy"));
      if (field && window.navigator && navigator.clipboard) {
        navigator.clipboard.writeText(field.value).then(function () {
          var label = copy.textContent;
          copy.textContent = "Copied";
          setTimeout(function () { copy.textContent = label; }, 1500);
        }, function () {});
      }
      return;
    }

    // On the upload page, choosing public turns camera details and location
    // off, and choosing a closed level turns them back on. Either can still be
    // ticked by hand afterwards.
    if (target.name === "visibility" && target.type === "radio" && target.form) {
      var boxes = target.form.querySelectorAll("input[data-off-when-public]");
      for (var i = 0; i < boxes.length; i++) boxes[i].checked = target.value !== "public";
    }
  }

  function onFocus(event) {
    var target = event.target;
    if (target && target.hasAttribute && target.hasAttribute("data-select-on-focus") && target.select) target.select();
  }

  function onInput(event) {
    var target = event.target;
    if (target && target.getAttribute && target.getAttribute("data-table-filter")) filterTable(target);
  }

  document.addEventListener("submit", onSubmit, true);
  document.addEventListener("click", onClick);
  document.addEventListener("focusin", onFocus);
  document.addEventListener("input", onInput);
  document.addEventListener("DOMContentLoaded", function () {
    var box = dialog();
    if (box) box.addEventListener("close", function () { pending = null; });
    if (!document.querySelectorAll) return;
    var filters = document.querySelectorAll("[data-table-filter]");
    for (var i = 0; i < filters.length; i++) filterTable(filters[i]);
  });

  if (typeof window !== "undefined") {
    window.imvault = { matches: matches, summary: summary, onSubmit: onSubmit, onClick: onClick, onFocus: onFocus, settle: settle };
  }
})();
