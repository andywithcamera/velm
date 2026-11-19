// Node tests for the canvas document transforms (Design Vision D3/D4).
// No build step: `node --test web/static/js/designer-canvas.test.js` imports
// the same file the browser loads.

const test = require("node:test");
const assert = require("node:assert/strict");
const canvas = require("./designer-canvas.js");

function doc(blocks) {
  return { blocks: blocks };
}

function keys(document) {
  return document.blocks.map((block) => block.key).join(",");
}

const base = () => doc([
  { key: "t", parent: "", kind: "trigger", position: 0 },
  { key: "i", parent: "", kind: "control", type: "if", position: 10 },
  { key: "a", parent: "i", kind: "task", position: 10 },
  { key: "b", parent: "i", kind: "task", position: 20 },
  { key: "x", parent: "", kind: "task", position: 20 },
  { key: "y", parent: "", kind: "task", position: 30 }
]);

test("dropping a block on itself is a no-op", () => {
  assert.deepEqual(canvas.resolveDrop(base(), "x", "x", "into"), { noop: true });
});

test("dropping a block into its own descendant is refused", () => {
  assert.equal(canvas.resolveDrop(base(), "i", "a", "into").error,
    "A block cannot be dropped inside itself");
});

test("a same-scope reorder changes order and positions only", () => {
  const document = base();
  const result = canvas.resolveDrop(document, "y", "x", "before");
  assert.equal(result.moved, true);
  assert.equal(keys(document), "t,i,a,b,y,x");
  const x = document.blocks.find((block) => block.key === "x");
  const y = document.blocks.find((block) => block.key === "y");
  assert.equal(y.position, 20);
  assert.equal(x.position, 30);
});

test("moving a control block moves its whole subtree and keeps internal links", () => {
  const document = base();
  const result = canvas.resolveDrop(document, "i", "y", "after");
  assert.equal(result.moved, true);
  assert.equal(keys(document), "t,x,y,i,a,b");
  assert.equal(document.blocks.find((block) => block.key === "a").parent, "i");
  assert.equal(document.blocks.find((block) => block.key === "b").parent, "i");
});

test("dropping into a control block reparents to its last position", () => {
  const document = base();
  const result = canvas.resolveDrop(document, "x", "i", "into");
  assert.equal(result.moved, true);
  const x = document.blocks.find((block) => block.key === "x");
  assert.equal(x.parent, "i");
  assert.equal(x.position, 30);
});

test("dropping to the canvas root clears the parent", () => {
  const document = base();
  const result = canvas.resolveDrop(document, "a", "", "root");
  assert.equal(result.moved, true);
  const a = document.blocks.find((block) => block.key === "a");
  assert.equal(a.parent, "");
});
