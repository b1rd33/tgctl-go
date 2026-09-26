import {
  linkTarget,
  entireRectVisible,
  activeAd,
  clickFlags,
  entitySegments,
} from "./logic.js";

const $ = (id) => document.getElementById(id);
let token = location.hash.slice(1);
history.replaceState(null, "", location.pathname);
let page,
  ad,
  closed = false,
  busy = false,
  blobs = [],
  viewScheduled = false;
const viewed = new Set();
const dialog = $("dialog");
function node(tag, text, className) {
  const n = document.createElement(tag);
  if (text !== undefined) n.textContent = text;
  if (className) n.className = className;
  return n;
}
function button(text, handler, className) {
  const n = node("button", text, className);
  n.type = "button";
  n.addEventListener("click", (e) => {
    if (e.isTrusted) handler(e);
  });
  return n;
}
function notice(text = "", error = false) {
  $("status").textContent = text;
  $("status").className = error ? "error" : "";
  $("status").hidden = !text;
}
async function api(path, body = {}, binary = false) {
  if (closed || !token)
    throw Error("This reader is closed. Start a new reader from the CLI.");
  const response = await fetch("/api/" + path, {
    method: "POST",
    headers: {
      Authorization: "Bearer " + token,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
    cache: "no-store",
    credentials: "omit",
    signal: AbortSignal.timeout(50000),
  });
  if (!response.ok) {
    if (response.status === 410)
      throw Error(
        "This page or advertisement expired. Refresh, or start a new reader.",
      );
    if (response.status === 403)
      throw Error(
        "Reader access was denied. Open the private URL from the CLI again.",
      );
    if (response.status === 409)
      throw Error(
        "This action was already attempted or is not ready. It will not be repeated.",
      );
    const detail = await response.json().catch(() => null);
    throw Error(
      detail?.error ||
        "The Telegram request could not be confirmed. Refresh messages when ready; an uncertain action is never retried automatically.",
    );
  }
  return binary ? response.blob() : response.json();
}
function clearBlobs() {
  for (const url of blobs) URL.revokeObjectURL(url);
  blobs = [];
}
function hideContent(message) {
  ad = null;
  $("content").hidden = true;
  notice(message, true);
}
function closeDialog() {
  dialog.close();
  scheduleView();
}
function showDialog(title, content, actions = []) {
  $("dialog-title").textContent = title;
  $("dialog-body").replaceChildren(
    typeof content === "string" ? node("p", content) : content,
  );
  $("dialog-actions").replaceChildren(
    ...actions,
    button(actions.length ? "Cancel" : "Close", closeDialog),
  );
  if (!dialog.open) dialog.showModal();
  $("dialog-actions").querySelector("button")?.focus();
}
dialog.addEventListener("close", scheduleView);
async function event(kind, flags = {}, option = "") {
  if (!activeAd(ad))
    throw Error("Advertisement expired. Refresh before interacting.");
  return api("event", {
    ad: ad.id,
    event: crypto.randomUUID(),
    action: kind,
    option,
    ...flags,
  });
}
function actionError(error, card) {
  if (!card.isConnected) return;
  let label = card.querySelector(".action-notice");
  if (!label) {
    label = node("p", "", "action-notice");
    label.setAttribute("role", "status");
    card.append(label);
  }
  label.textContent = error.message;
}
function openLink(raw, flags, card) {
  if (!activeAd(ad)) {
    actionError(
      Error("Advertisement expired. Refresh before interacting."),
      card,
    );
    return;
  }
  const target = linkTarget(raw);
  const open = () => {
    // Dispatch once per trusted interaction. Never wait for or retry uncertain engagement.
    event("click", flags).catch((err) => actionError(err, card));
    window.open(
      target.native ? target.href : target.original,
      "_blank",
      "noopener,noreferrer",
    );
  };
  if (target.confirmation) {
    const body = node("div");
    body.append(
      node("p", "Open this external website?"),
      node("p", target.original, "link-destination"),
    );
    showDialog("Leave the reader?", body, [
      button(
        "Open website",
        () => {
          closeDialog();
          open();
        },
        "primary",
      ),
    ]);
  } else open();
}
function renderEntities(text, entities, card) {
  const root = node("div", undefined, "ad-text");
  for (const segment of entitySegments(text, entities || [])) {
    let child = document.createTextNode(segment.text);
    for (const e of segment.entities) {
      const tags = {
        bold: "strong",
        italic: "em",
        underline: "u",
        strike: "s",
        code: "code",
        pre: "pre",
        blockquote: "blockquote",
      };
      let wrap;
      if (tags[e.type]) wrap = node(tags[e.type]);
      else if (e.type === "spoiler")
        wrap = button(
          "",
          (evt) => {
            evt.currentTarget.classList.add("revealed");
            evt.currentTarget.setAttribute("aria-label", "Revealed text");
            scheduleView();
          },
          "spoiler",
        );
      else if (
        e.type === "text_url" ||
        e.type === "url" ||
        e.type === "mention"
      ) {
        const raw =
          e.type === "text_url"
            ? e.url
            : e.type === "mention"
              ? "https://t.me/" + text.slice(e.offset + 1, e.offset + e.length)
              : text.slice(e.offset, e.offset + e.length);
        linkTarget(raw);
        wrap = node("a");
        wrap.href = raw;
        wrap.rel = "noopener noreferrer";
        // Always route links through a trusted, confirmed interaction, including keyboard activation.
        wrap.addEventListener("click", (evt) => {
          evt.preventDefault();
          if (evt.isTrusted)
            openLink(
              raw,
              { media: false, fullscreen: !!document.fullscreenElement },
              card,
            );
        });
        wrap.addEventListener("auxclick", (evt) => evt.preventDefault());
        wrap.addEventListener("contextmenu", (evt) => evt.preventDefault());
      } else {
        throw Error(
          "Unsupported sponsored link entity. Open this chat in Telegram.",
        );
      }
      if (e.type === "spoiler")
        wrap.setAttribute("aria-label", "Reveal hidden text");
      wrap.append(child);
      child = wrap;
    }
    root.append(child);
  }
  return root;
}
async function loadAsset(kind, file) {
  const blob = await api("asset", { ad: ad.id, part: kind }, true);
  const url = URL.createObjectURL(blob);
  blobs.push(url);
  const el = node(file.kind === "photo" ? "img" : "video");
  if (el.tagName === "IMG") {
    el.alt = kind === "avatar" ? "Sponsor avatar" : "Sponsored image";
    el.src = url;
    await el.decode();
  } else {
    el.playsInline = true;
    el.preload = "auto";
    el.muted = !file.has_sound;
    el.loop = !file.has_sound;
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(
        () => reject(Error("Sponsored video could not be loaded.")),
        30000,
      );
      el.addEventListener(
        "loadeddata",
        () => {
          clearTimeout(timeout);
          resolve();
        },
        { once: true },
      );
      el.addEventListener(
        "error",
        () => {
          clearTimeout(timeout);
          reject(Error("Sponsored video could not be decoded."));
        },
        { once: true },
      );
      el.src = url;
    });
    if (!file.has_sound) await el.play().catch(() => {});
  }
  return el;
}
async function report(card, option = "") {
  try {
    // The initial click and each option selection are separate, non-retried human actions.
    $("dialog-actions").replaceChildren();
    $("dialog-body").replaceChildren(node("p", "Sending report request…"));
    const result = await event("report", {}, option);
    if (result.state === "choose")
      showDialog(
        result.title || "Report ad",
        "Choose a reason:",
        result.options.map((o) =>
          button(o.text, () => report(card, o.id), "dialog-option"),
        ),
      );
    else if (result.state === "hidden" || result.state === "reported") {
      card.remove();
      ad = null;
      showDialog(
        "Report received",
        result.state === "hidden"
          ? "Telegram hid the advertisement."
          : "Telegram confirmed your report.",
      );
    } else throw Error("Telegram returned an unsupported report result.");
  } catch (error) {
    showDialog("Report not confirmed", error.message);
  }
}
async function renderAd(current) {
  ad = current;
  if (!ad) return;
  if (!activeAd(ad)) throw Error("Advertisement expired. Refresh to continue.");
  linkTarget(ad.url);
  const card = node("article", undefined, "ad");
  card.setAttribute("aria-label", "Sponsored message");
  const top = node("div", undefined, "ad-top");
  const avatar = button(
    ad.title.slice(0, 1),
    () =>
      openLink(
        ad.url,
        { media: false, fullscreen: !!document.fullscreenElement },
        card,
      ),
    "avatar",
  );
  avatar.setAttribute("aria-label", "Open " + ad.title);
  if (ad.avatar) avatar.replaceChildren(await loadAsset("avatar", ad.avatar));
  const heading = node("div", undefined, "ad-heading");
  const title = button(
    ad.title,
    () =>
      openLink(
        ad.url,
        { media: false, fullscreen: !!document.fullscreenElement },
        card,
      ),
    "ad-title",
  );
  if (page.kind === "bot")
    title.prepend(
      node(
        "span",
        (ad.recommended ? "Recommended" : "Ad") + " · ",
        "bot-label",
      ),
    );
  heading.append(title);
  if (page.kind !== "bot")
    heading.append(
      node("span", ad.recommended ? "Recommended" : "Sponsored", "ad-label"),
    );
  const menu = button(
    "···",
    () =>
      showDialog("Sponsored message", "", [
        button("Sponsor info", () =>
          showDialog(
            "Sponsor info",
            [ad.sponsor_info, ad.additional_info]
              .filter(Boolean)
              .join("\n\n") ||
              "No additional sponsor information was supplied.",
          ),
        ),
        ...(ad.can_report
          ? [
              button("Report ad", () =>
                showDialog(
                  "Report this ad?",
                  "Telegram will provide the available report reasons.",
                  [button("Continue", () => report(card), "primary")],
                ),
              ),
            ]
          : []),
      ]),
    "ad-menu",
  );
  menu.setAttribute("aria-label", "Advertisement options");
  if (page.kind === "bot") {
    top.append(heading, avatar, menu);
    card.classList.add("bot-ad");
    card.tabIndex = 0;
    card.setAttribute("role", "link");
    card.setAttribute("aria-label", "Open sponsored bot advertisement");
    const activate = (evt) => {
      if (evt.isTrusted && !evt.target.closest("button,a"))
        openLink(ad.url, { media: false, fullscreen: false }, card);
    };
    card.addEventListener("click", activate);
    card.addEventListener("keydown", (evt) => {
      if (evt.target === card && evt.key === "Enter") activate(evt);
    });
  } else top.append(avatar, heading, menu);
  card.append(top);
  const text = renderEntities(ad.text, ad.entities, card);
  card.append(text);
  if (ad.colors?.length) {
    if (!ad.colors.every((c) => /^#[a-f0-9]{6}$/i.test(c)))
      throw Error("Unsupported sponsor palette.");
    card.style.borderLeft = "4px solid " + ad.colors[0];
    heading.style.borderImage =
      "linear-gradient(" + ad.colors.join(",") + ") 1";
    heading.style.borderLeft = "3px solid";
    heading.style.paddingLeft = "9px";
  }
  if (ad.media) {
    const wrap = node("div", undefined, "ad-media");
    const media = await loadAsset("media", ad.media);
    const click = button(
      "",
      () => {
        const flags = clickFlags(
          ad.media.kind,
          ad.media.has_sound,
          !!document.fullscreenElement,
          "media",
        );
        if (flags.openLink)
          openLink(
            ad.url,
            { media: flags.media, fullscreen: flags.fullscreen },
            card,
          );
        else {
          if (!activeAd(ad)) return;
          card
            .requestFullscreen()
            .then(() => media.play())
            .catch((err) =>
              actionError(Error("Fullscreen video is unavailable."), card),
            );
          event("click", { media: true, fullscreen: false }).catch((err) =>
            actionError(err, card),
          );
        }
      },
      "media-click",
    );
    click.setAttribute(
      "aria-label",
      ad.media.has_sound ? "Play sponsored video" : "Open sponsored media link",
    );
    click.append(media);
    wrap.append(click);
    if (ad.media.kind !== "photo")
      wrap.append(
        button(
          "Expand video",
          () => {
            if (!activeAd(ad)) return;
            card
              .requestFullscreen()
              .then(() => media.play())
              .catch(() =>
                actionError(Error("Fullscreen video is unavailable."), card),
              );
          },
          "expand",
        ),
      );
    card.append(wrap);
  }
  if (ad.button)
    card.append(
      button(
        ad.button,
        () =>
          openLink(
            ad.url,
            { media: false, fullscreen: !!document.fullscreenElement },
            card,
          ),
        "ad-cta",
      ),
    );
  $(page.kind === "bot" ? "bot-ad" : "channel-ad").append(card);
}
function renderMessages(messages) {
  const list = $("messages");
  list.replaceChildren();
  let date = "";
  for (const m of messages) {
    const d = new Date(m.date);
    const label = Number.isNaN(d.valueOf())
      ? "Unknown date"
      : d.toLocaleDateString(undefined, {
          month: "long",
          day: "numeric",
          year: "numeric",
        });
    if (label !== date) {
      list.append(node("div", label, "divider"));
      date = label;
    }
    const row = node("article", undefined, "message");
    const time = node(
      "time",
      Number.isNaN(d.valueOf())
        ? ""
        : d.toLocaleTimeString(undefined, {
            hour: "2-digit",
            minute: "2-digit",
          }),
    );
    time.dateTime = m.date;
    const meta = node("div", undefined, "message-meta");
    meta.append(
      node(
        "strong",
        m.author || (page.kind === "channel" ? page.title : "Message"),
      ),
      time,
    );
    row.append(meta, node("p", m.deleted ? "Message deleted" : m.text));
    if (m.media)
      row.append(
        node(
          "p",
          m.media.replaceAll("_", " ") +
            " · Open in Telegram to view this attachment.",
          "media-note",
        ),
      );
    list.append(row);
  }
  if (!messages.length) list.append(node("p", "No messages in this chat."));
}
async function load(older = false) {
  if (busy || closed) return;
  busy = true;
  ad = null;
  $("older").disabled = true;
  $("refresh").disabled = true;
  $("content").hidden = true;
  notice("Loading messages…");
  if (dialog.open) dialog.close();
  clearBlobs();
  $("bot-ad").replaceChildren();
  $("channel-ad").replaceChildren();
  try {
    const incoming = await api("page", {
      offset: older ? page.next_offset : 0,
    });
    if (older) {
      const known = new Set(page.messages.map((m) => m.id));
      incoming.messages = [
        ...incoming.messages.filter((m) => !known.has(m.id)),
        ...page.messages,
      ];
    }
    page = incoming;
    $("title").textContent = page.title || "Selected chat";
    document.title = (page.title || "Chat") + " · tgctl reader";
    $("subtitle").textContent =
      (page.kind === "bot"
        ? "Bot"
        : page.kind === "channel"
          ? "Channel"
          : "Chat") +
      " · " +
      (older ? "Showing " + page.messages.length : "Latest " + page.limit) +
      " messages";
    renderMessages(page.messages);
    await renderAd(page.ad);
    $("content").hidden = false;
    notice();
    $("older").disabled = !page.next_offset;
    scheduleView();
  } catch (error) {
    hideContent(error.message);
  } finally {
    busy = false;
    $("refresh").disabled = closed;
  }
}
function scheduleView() {
  if (viewScheduled) return;
  viewScheduled = true;
  requestAnimationFrame(() => {
    viewScheduled = false;
    maybeView();
  });
}
function maybeView() {
  if (
    !activeAd(ad) ||
    viewed.has(ad.id) ||
    closed ||
    dialog.open ||
    document.fullscreenElement ||
    document.visibilityState !== "visible" ||
    !document.hasFocus() ||
    $("content").hidden
  )
    return;
  const text = document.querySelector(".ad-text");
  if (!text || text.querySelector(".spoiler:not(.revealed)")) return;
  const range = document.createRange();
  range.selectNodeContents(text);
  const rects = [...range.getClientRects()];
  if (
    !rects.length ||
    !rects.every((rect) => entireRectVisible(rect, innerWidth, innerHeight))
  )
    return;
  for (const rect of rects) {
    for (const [x, y] of [
      [rect.left + 1, rect.top + 1],
      [rect.right - 1, rect.bottom - 1],
      [(rect.left + rect.right) / 2, (rect.top + rect.bottom) / 2],
    ]) {
      const at = document.elementFromPoint(x, y);
      if (!at || !(at === text || text.contains(at))) return;
    }
  }
  viewed.add(ad.id);
  const card = text.closest(".ad");
  event("view").catch((err) => actionError(err, card));
}
for (const type of [
  "scroll",
  "resize",
  "focus",
  "visibilitychange",
  "fullscreenchange",
])
  window.addEventListener(type, scheduleView, { passive: true });
document.addEventListener("visibilitychange", scheduleView);
document.addEventListener("fullscreenchange", scheduleView);
$("refresh").addEventListener("click", (e) => {
  if (e.isTrusted) load();
});
$("older").addEventListener("click", (e) => {
  if (e.isTrusted) load(true);
});
$("close").addEventListener("click", async (e) => {
  if (!e.isTrusted) return;
  try {
    await api("close");
  } catch {
    notice(
      "The local server could not confirm shutdown. Stop the CLI to close it immediately.",
      true,
    );
    return;
  }
  closed = true;
  token = "";
  clearBlobs();
  hideContent("Reader closed.");
  $("title").textContent = "Reader closed";
  $("subtitle").textContent = "Local session ended";
  $("refresh").disabled = true;
  $("older").disabled = true;
  $("close").disabled = true;
  if (dialog.open) dialog.close();
});
setInterval(() => {
  if (closed || busy) return;
  if (page && Date.now() >= Date.parse(page.expires_at)) {
    closed = true;
    token = "";
    clearBlobs();
    hideContent("Reader session expired. Start a new reader from the CLI.");
    $("older").disabled = true;
    $("refresh").disabled = true;
  } else if (ad && !activeAd(ad)) {
    clearBlobs();
    hideContent("Advertisement expired. Refresh to continue reading.");
    $("older").disabled = true;
  }
}, 1000);
if (/^[a-f0-9]{64}$/.test(token)) load();
else {
  token = "";
  hideContent(
    "Open the complete private URL from the CLI to access this reader.",
  );
  $("title").textContent = "Private local reader";
  $("close").disabled = true;
}
