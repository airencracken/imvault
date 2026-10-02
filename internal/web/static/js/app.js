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
    function picks() {
      return Array.prototype.slice.call(document.querySelectorAll('input[name="files"][form="bulk-tags"]'));
    }
    function updateSelection() {
      var selected = picks().filter(function (pick) { return pick.checked; }).length;
      bulkCount.textContent = selected + " selected";
      bulkSubmit.disabled = selected === 0;
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
    });
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
 * Alpine.js components.
 *
 * Alpine is loaded only on the pages that ask for it (the admin area, the
 * account and invitation pages), where a little client-side state earns its
 * keep: htmx owns every server round trip, and Alpine owns the state that never
 * needs one.
 * ------------------------------------------------------------------------- */

document.addEventListener("alpine:init", function () {
  // Confirmation for destructive actions. A trigger calls ask() with the form
  // to submit; accepting re-submits it so htmx makes the request it would have
  // made anyway, with the dialog only standing in the way.
  Alpine.store("confirm", {
    open: false,
    message: "",
    detail: "",
    action: "Confirm",
    form: null,

    ask: function (form, options) {
      this.form = form;
      this.message = options.message;
      this.detail = options.detail || "";
      this.action = options.action || "Confirm";
      this.open = true;
    },

    cancel: function () {
      this.open = false;
      this.form = null;
    },

    accept: function () {
      var form = this.form;
      this.open = false;
      this.form = null;
      if (!form) return;

      // Marking the form is what tells the click interceptor to stand aside,
      // so the resubmission reaches htmx instead of reopening the dialog.
      form.dataset.confirmed = "1";
      form.requestSubmit();
    },
  });

  // Client-side filtering of a server-rendered table. Doing this with htmx
  // would mean a request per keystroke to narrow a list that is already on the
  // page.
  Alpine.data("tableFilter", function () {
    return {
      q: "",
      matches: function (row) {
        var query = this.q.trim().toLowerCase();
        return !query || (row.dataset.search || "").indexOf(query) !== -1;
      },
      summary: function (table) {
        if (!table) return "";
        var rows = Array.prototype.slice.call(table.querySelectorAll("tr[data-search]"));
        if (!this.q.trim()) return rows.length + " total";
        var shown = rows.filter(this.matches, this).length;
        return shown + " of " + rows.length + " shown";
      },
    };
  });
});
