// Pure policy helpers shared by the UI and the offline tests. Offsets are UTF-16.
export function safeURL(raw) {
  if (typeof raw !== "string" || /[\u0000-\u0020\u007f\\]/u.test(raw))
    throw Error("Unsupported link");
  const url = new URL(raw);
  if (
    !["https:", "http:"].includes(url.protocol) ||
    url.username ||
    url.password ||
    !url.hostname
  )
    throw Error("Unsupported link");
  return url;
}
export function linkTarget(raw) {
  const url = safeURL(raw);
  const official =
    /(^|\.)(telegram\.(org|me|dog)|t\.me|te\.?legra\.ph|graph\.org|fragment\.com|telesco\.pe)$/i.test(
      url.hostname,
    );
  // Only map understood Telegram links. Never silently lose a bot start payload,
  // forum topic, post ID, or turn a reserved path into a username.
  if (
    ["t.me", "telegram.me", "telegram.dog"].includes(url.hostname) &&
    !url.port
  ) {
    const parts = url.pathname.split("/").filter(Boolean);
    if (parts[0] === "s") parts.shift();
    const reserved = new Set([
      "addemoji",
      "addlist",
      "addstickers",
      "addstyle",
      "addtheme",
      "auction",
      "auth",
      "boost",
      "call",
      "confirmphone",
      "contact",
      "giftcode",
      "invoice",
      "joinchat",
      "login",
      "m",
      "nft",
      "proxy",
      "setlanguage",
      "share",
      "socks",
      "web",
      "a",
      "k",
      "z",
    ]);
    const params = new URLSearchParams(url.search);
    let method = "resolve";
    const allowed = new Set([
      "start",
      "startgroup",
      "startapp",
      "game",
      "text",
      "profile",
      "direct",
      "single",
      "thread",
      "comment",
      "t",
      "task",
      "option",
      "choose",
      "admin",
    ]);
    for (const key of params.keys())
      if (!allowed.has(key))
        throw Error("Unsupported Telegram link. Open this chat in Telegram.");
    if (parts[0] === "c" && /^\d+$/.test(parts[1] || "") && parts.length >= 3) {
      method = "privatepost";
      params.set("channel", parts[1]);
      parts.splice(0, 2);
    } else if (
      /^[A-Za-z][A-Za-z0-9_]{3,31}$/.test(parts[0] || "") &&
      !reserved.has(parts[0].toLowerCase())
    ) {
      params.set("domain", parts.shift());
    } else
      throw Error("Unsupported Telegram link. Open this chat in Telegram.");
    if (parts.length > 2 || parts.some((p) => !/^\d+$/.test(p)))
      throw Error("Unsupported Telegram message link.");
    if (parts.length === 2) params.set("thread", parts.shift());
    if (parts.length) params.set("post", parts[0]);
    return {
      href: "tg://" + method + "?" + params.toString(),
      native: true,
      confirmation: false,
      original: url.href,
      host: url.host,
    };
  }
  return {
    href: url.href,
    native: false,
    confirmation: !official || !!url.port,
    original: url.href,
    host: url.host,
  };
}
export function entireRectVisible(rect, width, height) {
  return (
    rect.width > 0 &&
    rect.height > 0 &&
    rect.left >= 0 &&
    rect.top >= 0 &&
    rect.right <= width &&
    rect.bottom <= height
  );
}
export function activeAd(ad, now = Date.now()) {
  return !!ad && now < Date.parse(ad.expires_at);
}
export function clickFlags(kind, hasSound, fullscreen, surface) {
  const media = surface === "media";
  return {
    media,
    fullscreen: !!fullscreen,
    openLink: !media || kind === "photo" || !hasSound || !!fullscreen,
  };
}
export function entitySegments(text, entities = []) {
  const allowed = new Set([
    "bold",
    "italic",
    "underline",
    "strike",
    "spoiler",
    "code",
    "pre",
    "blockquote",
    "url",
    "text_url",
    "mention",
  ]);
  const boundary = (n) =>
    n === 0 ||
    n === text.length ||
    !(
      text.charCodeAt(n - 1) >= 0xd800 &&
      text.charCodeAt(n - 1) <= 0xdbff &&
      text.charCodeAt(n) >= 0xdc00 &&
      text.charCodeAt(n) <= 0xdfff
    );
  const marks = new Set([0, text.length]);
  for (const e of entities) {
    if (
      !allowed.has(e.type) ||
      !Number.isInteger(e.offset) ||
      !Number.isInteger(e.length) ||
      e.offset < 0 ||
      e.length <= 0 ||
      e.offset + e.length > text.length ||
      !boundary(e.offset) ||
      !boundary(e.offset + e.length)
    )
      throw Error("Unsupported sponsored formatting");
    if (e.type === "text_url") safeURL(e.url);
    marks.add(e.offset);
    marks.add(e.offset + e.length);
  }
  const sorted = [...marks].sort((a, b) => a - b);
  return sorted
    .slice(0, -1)
    .map((start, i) => ({
      text: text.slice(start, sorted[i + 1]),
      entities: entities.filter(
        (e) => e.offset <= start && e.offset + e.length >= sorted[i + 1],
      ),
    }));
}
