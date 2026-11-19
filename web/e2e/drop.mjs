// Playwright evidence for the canvas drag-and-drop slice (AC-25, AC-32,
// AC-34). Not part of the Go build: run it against a live dev server with
//   PLAYWRIGHT_BROWSERS_PATH=$HOME/.cache/ms-playwright node web/e2e/drop.mjs \
//     http://localhost:8080/task-definitions/<slug>
// It is data-independent: it only reads the page's own document field and
// asserts that an unknown-kind drop posts nothing.
import { chromium } from "playwright";

const url = process.argv[2];
if (!url) {
  console.error("usage: node web/e2e/drop.mjs <definition-url>");
  process.exit(2);
}

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
await page.goto(url);

// AC-25: during dragover between two blocks the indicator exists and is 2px.
const blocks = page.locator(".ui-canvas-block");
const blockCount = await blocks.count();
if (blockCount < 2) {
  throw new Error("need at least two blocks for the between-blocks case");
}
const second = await blocks.nth(1).boundingBox();
await page.mouse.move(second.x + 10, second.y + 2);
await page.evaluate(() => {
  const target = document.querySelectorAll(".ui-canvas-block")[1];
  const rect = target.getBoundingClientRect();
  target.dispatchEvent(new DragEvent("dragover", {
    bubbles: true, cancelable: true, clientY: rect.top + 1, clientX: rect.left + 10
  }));
});
const insert = page.locator(".ui-canvas-insert");
if ((await insert.count()) !== 1) { throw new Error("no single .ui-canvas-insert"); }
const height = await insert.evaluate((el) => el.getBoundingClientRect().height);
if (Math.round(height) !== 2) { throw new Error(`indicator is ${height}px, want 2px`); }
console.log("AC-25 ok: insertion indicator is 2px high");

// AC-32: under 900px the palette collapses and the toggle opens the same list.
await page.setViewportSize({ width: 880, height: 900 });
const toggle = page.locator("[data-palette-toggle]");
if (!(await toggle.isVisible())) { throw new Error("palette toggle is not visible below 900px"); }
const body = page.locator("#palette-body");
if (await body.isVisible()) { throw new Error("palette body is not collapsed below 900px"); }
await toggle.click();
if (!(await body.isVisible())) { throw new Error("toggle did not open the palette body"); }
console.log("AC-32 ok: palette collapses below 900px and the toggle reopens it");

// AC-34: an unknown kind posts nothing and leaves the document field intact.
await page.setViewportSize({ width: 1280, height: 900 });
const before = await page.locator("#canvas-document").inputValue();
let posts = 0;
page.on("request", (request) => {
  if (request.method() === "POST" && request.url().includes("/structure")) { posts += 1; }
});
await page.evaluate(() => {
  const tree = document.getElementById("canvas-tree");
  tree.dispatchEvent(new DragEvent("drop", {
    bubbles: true, cancelable: true,
    dataTransfer: (() => {
      const transfer = new DataTransfer();
      transfer.setData("application/x-velm-palette", JSON.stringify({ kind: "nonsense", label: "Nope" }));
      return transfer;
    })()
  }));
});
await page.waitForTimeout(200);
const after = await page.locator("#canvas-document").inputValue();
if (posts !== 0) { throw new Error(`invalid drop posted ${posts} times`); }
if (before !== after) { throw new Error("invalid drop rewrote the document field"); }
console.log("AC-34 ok: unknown kind posts nothing and the document is byte-identical");

await browser.close();
