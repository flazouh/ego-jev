// Runs inside `ego-browser nodejs`. Connects back to the ego-jev process over a Unix socket and serves
// page operations as JSON lines: {id, method, params} in, {id, result} or {id, error} out.
// The Go side replaces the config marker below with a JSON object before starting this script.
const CONFIG = __EGO_JEV_CONFIG__;

const net = await import("node:net");
const readline = await import("node:readline");

// ---- Functions passed to page.evaluate(). Each runs inside the web page and must be self-contained. ----

function observeInPage({ maxElements, maxText }) {
  const roles = ["button", "link", "checkbox", "radio", "switch", "tab", "menuitem", "menuitemcheckbox", "menuitemradio", "option", "combobox", "textbox", "searchbox", "spinbutton", "treeitem", "gridcell"];
  const selector = `a[href],button,input,textarea,select,summary,[contenteditable="true"],[contenteditable=""],${roles.map((r) => `[role="${r}"]`).join(",")}`;
  // Ids restart at e1 on every observation, so tags from an earlier observation are cleared before tagging again.
  const found = [];
  const walk = (root) => {
    for (const el of root.querySelectorAll("[data-ego-jev]")) el.removeAttribute("data-ego-jev");
    for (const el of root.querySelectorAll(selector)) found.push(el);
    for (const el of root.querySelectorAll("*")) if (el.shadowRoot) walk(el.shadowRoot);
  };
  walk(document);

  const state = (window.__egoJev = { nodes: new Map() });
  const visible = (el) => {
    if (el.closest('[aria-hidden="true"],[inert]')) return false;
    if (el.checkVisibility && !el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true })) return false;
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0 && r.bottom > 0 && r.right > 0 && r.top < innerHeight && r.left < innerWidth;
  };
  const text = (value, max = 90) => String(value ?? "").replace(/\s+/g, " ").trim().slice(0, max);
  const ownText = (label) => {
    const copy = label.cloneNode(true);
    copy.querySelectorAll("input,select,textarea,option").forEach((n) => n.remove());
    return copy.textContent;
  };
  const name = (el) => {
    const labelled = (el.getAttribute("aria-labelledby") || "").split(/\s+/).map((id) => document.getElementById(id)?.innerText).filter(Boolean).join(" ");
    return text(
      labelled || el.getAttribute("aria-label") || [...(el.labels || [])].map(ownText).join(" ") ||
        (["button", "submit", "reset"].includes(el.type) ? el.value : "") || el.getAttribute("alt") ||
        (el.tagName === "INPUT" || el.tagName === "SELECT" ? "" : el.innerText) ||
        el.getAttribute("title") || el.getAttribute("placeholder") || el.querySelector("img[alt]")?.alt || "",
    );
  };
  const roleOf = (el) => {
    const explicit = el.getAttribute("role");
    if (roles.includes(explicit)) return explicit;
    const tag = el.tagName;
    if (tag === "A") return "link";
    if (tag === "BUTTON" || tag === "SUMMARY") return "button";
    if (tag === "SELECT") return "combobox";
    if (tag === "TEXTAREA" || el.isContentEditable) return "textbox";
    if (tag === "INPUT") {
      if (["checkbox", "radio"].includes(el.type)) return el.type;
      if (["button", "submit", "reset", "image"].includes(el.type)) return "button";
      if (el.type === "search") return "searchbox";
      if (el.type === "number") return "spinbutton";
      return "textbox";
    }
    return null;
  };

  const elements = [];
  const included = new Set();
  for (const el of found) {
    if (elements.length >= maxElements) break;
    if (el.tagName === "INPUT" && ["hidden", "password", "file"].includes(el.type)) continue;
    if (el.matches(":disabled") || el.closest('[aria-disabled="true"]') || !visible(el)) continue;
    const role = roleOf(el);
    if (!role) continue;
    const parent = el.parentElement?.closest(selector);
    if (parent && included.has(parent) && !["INPUT", "SELECT", "TEXTAREA"].includes(el.tagName)) continue;
    const id = `e${elements.length + 1}`;
    state.nodes.set(id, el);
    el.setAttribute("data-ego-jev", id);
    included.add(el);
    const record = { id, role, label: name(el) || role };
    const editable =
      !el.readOnly && (el.isContentEditable || el.tagName === "TEXTAREA" || (el.tagName === "INPUT" && !["checkbox", "radio", "button", "submit", "reset", "image", "range", "color"].includes(el.type)));
    if (editable) record.editable = true;
    if ("value" in el && el.tagName !== "BUTTON" && el.tagName !== "SELECT") record.value = text(el.value, 120);
    else if (el.isContentEditable) record.value = text(el.innerText, 120);
    if (["checkbox", "radio"].includes(el.type)) record.checked = String(el.checked);
    for (const key of ["checked", "selected", "expanded"]) {
      const v = el.getAttribute(`aria-${key}`);
      if (v != null) record[key] = v;
    }
    if (el.tagName === "SELECT") {
      record.value = [...el.selectedOptions].map((o) => o.label).join(", ");
      record.options = [...el.options].filter((o) => !o.disabled).slice(0, 60).map((o) => ({ value: o.value, label: text(o.label), selected: o.selected }));
    }
    const scope = el.parentElement?.closest('[role="dialog"],[role="group"],[role="radiogroup"],[role="listbox"],[role="menu"],fieldset,form[aria-label],dialog');
    const scopeName = scope && text(scope.getAttribute("aria-label") || scope.querySelector(":scope > legend")?.innerText || "", 60);
    if (scopeName && scopeName !== record.label) record.context = scopeName;
    if (el.tagName === "A") record.href = el.getAttribute("href")?.slice(0, 120);
    elements.push(record);
  }

  const words = [];
  let length = 0;
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  const range = document.createRange();
  for (let node = walker.nextNode(); node && length < maxText; node = walker.nextNode()) {
    const value = node.textContent.replace(/\s+/g, " ").trim();
    const parent = node.parentElement;
    if (!value || !parent || parent.closest("script,style,noscript,template")) continue;
    range.selectNodeContents(node);
    const r = range.getBoundingClientRect();
    if (r.width > 0 && r.height > 0 && r.bottom > 0 && r.top < innerHeight && r.right > 0 && r.left < innerWidth) {
      words.push(value);
      length += value.length + 1;
    }
  }
  const scroller = document.scrollingElement || document.documentElement;
  return {
    url: location.href,
    title: document.title,
    text: words.join("\n").slice(0, maxText),
    elements,
    viewport: { width: innerWidth, height: innerHeight },
    scroll: { up: scroller.scrollTop > 0, down: scroller.scrollTop + innerHeight < scroller.scrollHeight - 2 },
  };
}

function locateInPage({ id, prepareText }) {
  const el = window.__egoJev?.nodes.get(id);
  if (!el?.isConnected) return { ok: false, reason: "element is gone" };
  let r = el.getBoundingClientRect();
  if (r.top < 0 || r.bottom > innerHeight || r.left < 0 || r.right > innerWidth) {
    el.scrollIntoView({ block: "center", inline: "center" });
    r = el.getBoundingClientRect();
  }
  const x = r.left + Math.min(r.width / 2, r.width - 1);
  const y = r.top + Math.min(r.height / 2, r.height - 1);
  const root = el.getRootNode();
  const hit = (typeof root.elementFromPoint === "function" ? root : document).elementFromPoint(x, y);
  const hitOk = !!hit && (el === hit || el.contains(hit) || [...(el.labels || [])].some((l) => l.contains(hit)));
  if (prepareText) {
    el.focus();
    if (typeof el.select === "function") el.select();
    else if (el.isContentEditable) {
      const sel = getSelection();
      sel.removeAllRanges();
      const all = document.createRange();
      all.selectNodeContents(el);
      sel.addRange(all);
    }
  }
  return { ok: true, x, y, hitOk };
}

function settleInPage({ quietMs, maxMs }) {
  return new Promise((resolve) => {
    const started = performance.now();
    let last = performance.now();
    const observer = new MutationObserver(() => (last = performance.now()));
    observer.observe(document, { subtree: true, childList: true, attributes: true, characterData: true });
    const tick = () => {
      const now = performance.now();
      if (now - last >= quietMs || now - started >= maxMs) {
        observer.disconnect();
        resolve(Math.round(now - started));
      } else setTimeout(tick, 30);
    };
    setTimeout(tick, 30);
  });
}

// ---- Actions. Raw CDP input skips ego's input pacing; ego's own methods are the fallback. ----

const selectorFor = (id) => `[data-ego-jev="${id}"]`;

async function mouse(page, type, x, y, extra = {}) {
  await page.cdp("Input.dispatchMouseEvent", { type, x, y, ...extra });
}

async function click(page, id) {
  const spot = await page.evaluate(locateInPage, { id });
  if (!spot.ok) throw new Error(`cannot click ${id}: ${spot.reason}`);
  if (!spot.hitOk) {
    await page.click(selectorFor(id));
    return "ego-click";
  }
  await mouse(page, "mouseMoved", spot.x, spot.y);
  await mouse(page, "mousePressed", spot.x, spot.y, { button: "left", clickCount: 1 });
  await mouse(page, "mouseReleased", spot.x, spot.y, { button: "left", clickCount: 1 });
  return "cdp";
}

async function scroll(page, viewport, direction) {
  await mouse(page, "mouseWheel", viewport.width / 2, viewport.height / 2, { deltaX: 0, deltaY: direction * Math.round(viewport.height * 0.8) });
  return "cdp";
}

const ACTIONS = {
  CLICK: (page, { target }) => click(page, target.id),
  TYPE: async (page, { target, text }) => {
    const via = await click(page, target.id);
    await page.evaluate(locateInPage, { id: target.id, prepareText: true });
    await page.cdp("Input.insertText", { text });
    return via;
  },
  SELECT: async (page, { target }) => {
    await page.selectOption(selectorFor(target.id), { value: target.option.value });
    return "ego-select";
  },
  PRESS_ENTER: async (page) => {
    await page.keyboard.press("Enter");
    return "ego-key";
  },
  SCROLL_DOWN: (page, { viewport }) => scroll(page, viewport, 1),
  SCROLL_UP: (page, { viewport }) => scroll(page, viewport, -1),
  WAIT: async () => {
    await new Promise((r) => setTimeout(r, 600));
    return "wait";
  },
};

async function settle(page) {
  try {
    await page.evaluate(settleInPage, { quietMs: 150, maxMs: 2000 });
  } catch {
    // A navigation destroyed the page context mid-wait.
    await page.waitForLoadState("domcontentloaded", { timeout: 8000 }).catch(() => {});
    await page.evaluate(settleInPage, { quietMs: 150, maxMs: 2000 }).catch(() => {});
  }
}

// ---- Server ----

const task = CONFIG.space != null ? await taskSpace(CONFIG.space) : await taskSpace(CONFIG.name);
const page = task.page(CONFIG.page);

const methods = {
  hello: async () => ({ spaceId: task.spaceId, page: page.label, url: await page.url() }),
  goto: async ({ url }) => {
    await page.goto(url, { waitUntil: "load" });
    return { url: await page.url() };
  },
  observe: (limits) => page.evaluate(observeInPage, limits),
  perform: async ({ op, target, text, viewport }) => {
    const action = ACTIONS[op];
    if (!action) throw new Error(`unknown operation ${op}`);
    const via = await action(page, { target, text, viewport });
    await settle(page);
    return { via };
  },
  // observeInPage already tagged every listed element, held ones included.
  markHeld: async ({ ids }) => ({ selectors: ids.map(selectorFor) }),
  finish: async ({ close }) => {
    if (close) await task.finish({ keep: [] });
    return {};
  },
};

const socket = net.createConnection(CONFIG.socket);
await new Promise((resolve, reject) => {
  socket.once("connect", resolve);
  socket.once("error", reject);
});
// The Go side may close first after a timeout. Treat that as the end of the session, not a crash.
socket.on("error", (error) => console.error(`ego-jev bridge socket: ${error.message}`));
for await (const line of readline.createInterface({ input: socket })) {
  const { id, method, params } = JSON.parse(line);
  let reply;
  try {
    const fn = methods[method];
    if (!fn) throw new Error(`unknown method ${method}`);
    reply = { id, result: await fn(params ?? {}) };
  } catch (error) {
    reply = { id, error: String(error?.message ?? error) };
  }
  if (socket.destroyed) break;
  socket.write(JSON.stringify(reply) + "\n");
  if (method === "finish") break;
}
socket.end();
