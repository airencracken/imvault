// imvault client-side enhancements: drag-and-drop, clipboard paste and the
// small amount of feedback the server-rendered pages cannot provide.
(function () {
  "use strict";

  var input = document.getElementById("file-input");
  var dropzone = document.getElementById("dropzone");
  var count = document.getElementById("file-count");
  var form = document.getElementById("upload-form");

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

  // Track which files the user has staged. Assigning to input.files requires a
  // DataTransfer, which every browser that supports drag-and-drop provides.
  var staged = [];
  var transfer = new DataTransfer();

  function stage(files) {
    for (var i = 0; i < files.length; i++) {
      var f = files[i];
      if (!f.type || f.type.indexOf("image/") !== 0) continue;
      transfer.items.add(f);
    }
    input.files = transfer.files;
    render(input.files);
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
    stage(picked);
    if (window.htmx) window.htmx.trigger(form, "submit");
  });

  // Clear the selection once an upload completes so the next one starts fresh.
  document.body.addEventListener("htmx:afterRequest", function (e) {
    if (!form || e.detail.elt !== form) return;
    if (e.detail.successful) {
      transfer = new DataTransfer();
      input.files = transfer.files;
      render(null);
    }
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
