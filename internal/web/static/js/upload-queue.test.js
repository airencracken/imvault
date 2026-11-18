// SPDX-License-Identifier: AGPL-3.0-or-later
const assert = require("node:assert/strict");
const { test } = require("node:test");
const { batchesFor } = require("./upload-queue.js");

test("queue batches preserve order and bound each request", () => {
  for (let count = 0; count < 100; count++) {
    const files = Array.from({length: count}, (_, i) => ({name: String(i), size: (i % 10) + 1}));
    const batches = batchesFor(files, 20, 41);
    assert.deepEqual(batches.flat(), files);
    for (const batch of batches) {
      assert.ok(batch.length > 0 && batch.length <= 20);
      assert.ok(batch.reduce((sum, file) => sum + file.size, 0) <= 41);
    }
  }
});

test("a large file goes through the normal server checks on its own", () => {
  const little = {size: 1}, large = {size: 1025};
  assert.deepEqual(batchesFor([little, large, little], 20, 1024), [[little], [large], [little]]);
  assert.equal(batchesFor(Array(21).fill(little), 20, 1024).length, 2);
});
