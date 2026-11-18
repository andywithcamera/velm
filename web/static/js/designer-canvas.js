// Process designer canvas: pure document transforms (Design Vision D3/D4).
//
// The canvas keeps one document — the flat block list the server stores — and
// this file is the only place a drag is resolved. It is a plain script with no
// build step: the browser gets window.VelmCanvas, and node --test imports the
// same functions, so the drop cases are tested against the code that ships.
(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
  } else {
    root.VelmCanvas = api;
  }
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  function blockIndex(doc, key) {
    if (!key) { return -1; }
    for (var i = 0; i < doc.blocks.length; i++) {
      if (doc.blocks[i].key === key) { return i; }
    }
    return -1;
  }

  function isDescendant(doc, key, ancestorKey) {
    var seen = {};
    var index = blockIndex(doc, key);
    if (index < 0) { return false; }
    var block = doc.blocks[index];
    while (block && block.parent) {
      if (block.parent === ancestorKey) { return true; }
      if (seen[block.parent]) { return false; }
      seen[block.parent] = true;
      var parentIndex = blockIndex(doc, block.parent);
      if (parentIndex < 0) { return false; }
      block = doc.blocks[parentIndex];
    }
    return false;
  }

  function subtreeEnd(doc, index) {
    var end = index + 1;
    while (end < doc.blocks.length && isDescendant(doc, doc.blocks[end].key, doc.blocks[index].key)) {
      end++;
    }
    return end;
  }

  // Positions are 10, 20, 30… per scope, in document order. Start is pinned
  // at 0 (Design Vision D6).
  function renumber(doc) {
    var next = {};
    for (var i = 0; i < doc.blocks.length; i++) {
      var parent = doc.blocks[i].parent || "";
      if (doc.blocks[i].kind === "trigger") {
        doc.blocks[i].position = 0;
        continue;
      }
      next[parent] = (next[parent] || 0) + 10;
      doc.blocks[i].position = next[parent];
    }
  }

  // relocate lifts a block and its subtree out and reinserts it under
  // newParent, before the anchor's subtree or after it. Internal parent links
  // inside the subtree are untouched, which is what makes a subtree move move
  // the whole subtree.
  function relocate(doc, key, newParent, anchorKey, before) {
    var index = blockIndex(doc, key);
    if (index < 0) { return false; }
    var end = subtreeEnd(doc, index);
    var subtree = doc.blocks.slice(index, end);
    var rest = doc.blocks.slice(0, index).concat(doc.blocks.slice(end));
    subtree[0].parent = newParent;
    var insertAt = rest.length;
    if (anchorKey) {
      var anchorIndex = blockIndex({blocks: rest}, anchorKey);
      if (anchorIndex >= 0) {
        insertAt = before ? anchorIndex : subtreeEnd({blocks: rest}, anchorIndex);
      }
    }
    doc.blocks = rest.slice(0, insertAt).concat(subtree, rest.slice(insertAt));
    renumber(doc);
    return true;
  }

  // resolveDrop turns a drag into either a rewritten document, a no-op (a
  // block dropped on itself) or a readable refusal (a block dropped into its
  // own descendant). Callers must not post on error or noop.
  function resolveDrop(doc, dragKey, targetKey, mode) {
    if (!dragKey || blockIndex(doc, dragKey) < 0) {
      return {error: "That block is no longer in the document"};
    }
    if (targetKey && targetKey === dragKey) { return {noop: true}; }
    if (targetKey && isDescendant(doc, targetKey, dragKey)) {
      return {error: "A block cannot be dropped inside itself"};
    }
    if (mode === "root") {
      return {doc: doc, moved: relocate(doc, dragKey, "", "", false)};
    }
    var targetIndex = blockIndex(doc, targetKey);
    if (targetIndex < 0) {
      return {error: "That drop target is no longer in the document"};
    }
    var target = doc.blocks[targetIndex];
    if (mode === "into") {
      if (target.kind !== "control") {
        return {error: "Only a control block can hold a scope"};
      }
      return {doc: doc, moved: relocate(doc, dragKey, target.key, target.key, false)};
    }
    var parent = target.parent || "";
    return {doc: doc, moved: relocate(doc, dragKey, parent, target.key, mode === "before")};
  }

  return {
    blockIndex: blockIndex,
    isDescendant: isDescendant,
    subtreeEnd: subtreeEnd,
    renumber: renumber,
    relocate: relocate,
    resolveDrop: resolveDrop
  };
});
