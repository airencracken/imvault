// SPDX-License-Identifier: AGPL-3.0-or-later
(function () {
  "use strict";

  function batchesFor(files, maxFiles, maxBytes) {
    const batches = [];
    let batch = [];
    let bytes = 0;
    for (const file of files) {
      if (batch.length && (batch.length >= maxFiles || bytes + file.size > maxBytes)) {
        batches.push(batch);
        batch = [];
        bytes = 0;
      }
      batch.push(file);
      bytes += file.size;
    }
    if (batch.length) batches.push(batch);
    return batches;
  }

  if (typeof module !== "undefined") module.exports = { batchesFor };
  if (typeof document === "undefined" || !window.htmx || typeof DataTransfer === "undefined") return;

  const form = document.querySelector("#upload-form[data-batch-files]");
  if (!form) return;
  const input = document.getElementById("file-input");
  const results = document.getElementById("results");
  const status = document.getElementById("upload-batch-status");
  const maxFiles = Number(form.dataset.batchFiles);
  const maxBytes = Number(form.dataset.batchBytes);
  let uploading = false;
  let paused = null;

  form.addEventListener("submit", function (event) {
    if (uploading) {
      event.preventDefault();
      event.stopImmediatePropagation();
      return;
    }
    const files = Array.from(input.files);
    const resume = paused && paused.files.length === files.length && paused.files.every((file, index) => file === files[index]);
    const batches = batchesFor(files, maxFiles, maxBytes);
    if (!resume) {
      paused = null;
      if (batches.length <= 1) {
        status.hidden = true;
        return;
      }
    }
    event.preventDefault();
    event.stopImmediatePropagation();
    upload(batches);
  }, true);

  async function upload(batches) {
    const data = new FormData(form);
    data.delete("files");
    const submit = form.querySelector('button[type="submit"]');
    uploading = true;
    input.disabled = submit.disabled = true;
    if (!paused) results.replaceChildren();
    status.hidden = false;
    let {saved, skipped, failed} = paused || {saved: 0, skipped: 0, failed: 0};
    let completed = 0;
    try {
      for (const batch of batches) {
        status.textContent = `Uploading batch ${completed + 1} of ${batches.length}…`;
        data.delete("files");
        for (const file of batch) data.append("files", file);
        const response = await fetch(form.action, {
          method: "POST", body: data, headers: { "HX-Request": "true" },
        });
        const html = await response.text();
        const parsed = new DOMParser().parseFromString(html, "text/html").body;
        const result = parsed.querySelector(".upload-result[data-saved]");
        if (!response.ok || response.redirected || !result) {
          if (!response.redirected && result) window.htmx.swap(results, result.outerHTML, {swapStyle: "beforeend"});
          const retry = response.headers.get("Retry-After");
          throw new Error(retry ? `Try again in ${retry} seconds.` :
            "Review the gallery or choose Skip files already in your library before retrying.");
        }
        window.htmx.swap(results, result.outerHTML, {swapStyle: "beforeend"});
        saved += Number(result.dataset.saved);
        skipped += Number(result.dataset.skipped);
        failed += Number(result.dataset.failed);
        completed++;
      }
      status.textContent = `${saved} uploaded, ${skipped} already in your library, ${failed} failed.`;
    } catch (error) {
      status.textContent = `Paused after ${completed} of ${batches.length} batches. ${saved} uploaded, ${skipped} skipped, ${failed} failed. ${error.message}`;
    } finally {
      const pending = new DataTransfer();
      for (const batch of batches.slice(completed)) {
        for (const file of batch) pending.items.add(file);
      }
      input.files = pending.files;
      paused = completed < batches.length ? {files: Array.from(input.files), saved, skipped, failed} : null;
      input.disabled = submit.disabled = false;
      input.dispatchEvent(new Event("change", {bubbles: true}));
      uploading = false;
    }
  }
})();
