import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
// Load as a module without imposing a package.json on a Go repository.
const source = await readFile(
  new URL("./web/logic.js", import.meta.url),
  "utf8",
);
const {
  safeURL,
  linkTarget,
  entireRectVisible,
  activeAd,
  clickFlags,
  entitySegments,
} = await import(
  "data:text/javascript;base64," + Buffer.from(source).toString("base64")
);
test("only safe explicit web links; exact official-domain matching", () => {
  for (const raw of [
    "javascript:alert(1)",
    "data:text/html,x",
    "//evil.test",
    "https://name:pass@example.com",
    "https://t.me\\@evil.test",
    "https://evil.test/\n",
  ])
    assert.throws(() => safeURL(raw));
  assert.equal(linkTarget("https://telegram.org.evil.test").confirmation, true);
  assert.equal(linkTarget("https://telegram.org:8443").confirmation, true);
  assert.equal(linkTarget("https://t.me/example").native, true);
  assert.equal(linkTarget("https://t.me/example/123?single").native, true);
  assert.match(linkTarget("https://t.me/example/123?single").href, /post=123/);
  assert.match(
    linkTarget("https://t.me/example?start=hello").href,
    /start=hello/,
  );
  assert.match(
    linkTarget("https://t.me/c/123/10/20").href,
    /privatepost\?channel=123&thread=10&post=20/,
  );
  assert.throws(() => linkTarget("https://t.me/proxy?server=example.com"));
  assert.throws(() => linkTarget("https://t.me/example?domain=someone_else"));
});
test("full rectangle visibility excludes partial and empty text", () => {
  const r = { left: 2, top: 2, right: 100, bottom: 50, width: 98, height: 48 };
  assert.equal(entireRectVisible(r, 100, 50), true);
  for (const patch of [
    { left: -1 },
    { top: -1 },
    { right: 101 },
    { bottom: 51 },
    { width: 0 },
    { height: 0 },
  ])
    assert.equal(entireRectVisible({ ...r, ...patch }, 100, 50), false);
  assert.equal(
    activeAd(
      { expires_at: "2026-01-01T00:00:00Z" },
      Date.parse("2026-01-01T00:00:00Z"),
    ),
    false,
  );
});
test("media flags distinguish sound, fullscreen and CTA", () => {
  assert.deepEqual(clickFlags("video", true, false, "media"), {
    media: true,
    fullscreen: false,
    openLink: false,
  });
  assert.deepEqual(clickFlags("video", true, true, "media"), {
    media: true,
    fullscreen: true,
    openLink: true,
  });
  assert.deepEqual(clickFlags("video", false, false, "media"), {
    media: true,
    fullscreen: false,
    openLink: true,
  });
  assert.deepEqual(clickFlags("video", true, true, "button"), {
    media: false,
    fullscreen: true,
    openLink: true,
  });
});
test("UTF-16 formatting is lossless and rejects unsupported or split entities", () => {
  const text = "A😀<script> & text";
  const entities = [
    { type: "bold", offset: 1, length: 2 },
    { type: "italic", offset: 0, length: 3 },
  ];
  const parts = entitySegments(text, entities);
  assert.equal(parts.map((p) => p.text).join(""), text);
  assert.equal(parts[1].entities.length, 2);
  for (const e of [
    { type: "bold", offset: 2, length: 1 },
    { type: "bold", offset: 0, length: 99 },
    { type: "customemoji", offset: 0, length: 1 },
  ])
    assert.throws(() => entitySegments(text, [e]));
});
