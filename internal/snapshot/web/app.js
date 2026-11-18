"use strict";
// Renders only through textContent so stored (untrusted) text can never become markup.
const data = JSON.parse(document.getElementById("data").textContent);
const view = document.getElementById("view");
const crumbs = document.getElementById("crumbs");
const TYPES = ["fact", "decision", "lesson", "task", "handoff", "preference", "finding", "discovery"];
const STATUSES = ["active", "superseded", "archived", "expired"];
const HANDOFF = ["in_progress", "blocked", "completed"];

function el(tag, attrs, ...kids) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (k === "class") node.className = v;
    else node.setAttribute(k, v);
  }
  for (const kid of kids) node.append(kid);
  return node;
}
const link = (href, text, cls) => el("a", { href, class: cls || "" }, text);
const enc = encodeURIComponent;

function projectById(key) { return data.projects.find((p) => p.key === key); }
function memoryById(id) {
  for (const p of data.projects) {
    const m = p.memories.find((x) => x.id === id);
    if (m) return { m, p };
  }
  return null;
}
function route() {
  const parts = location.hash.replace(/^#\/?/, "").split("/").map(decodeURIComponent);
  const [name, key] = [parts[0], parts[1]];
  const query = new URLSearchParams((location.hash.split("?")[1] || ""));
  view.replaceChildren();
  if (name === "p" && key) {
    const p = projectById(key.split("?")[0]);
    if (!p) return notFound();
    return parts[2] && parts[2].startsWith("list") ? listView(p, query) : projectView(p);
  }
  if (name === "m" && key) {
    const hit = memoryById(key);
    return hit ? memoryView(hit.m, hit.p) : notFound();
  }
  projectsView();
}
function setCrumbs(...items) {
  crumbs.replaceChildren();
  items.forEach((item, i) => {
    if (i) crumbs.append(" / ");
    crumbs.append(item);
  });
}
function notFound() {
  setCrumbs(link("#/", "Projects"));
  view.append(el("p", {}, "Not in this snapshot."));
}

function projectsView() {
  setCrumbs(el("span", {}, "Projects"));
  const box = el("input", { type: "search", placeholder: "Filter projects", "aria-label": "Filter projects" });
  const list = el("div");
  const draw = () => {
    list.replaceChildren();
    const q = box.value.toLowerCase();
    for (const p of data.projects) {
      const hay = (p.key + " " + (p.project.local_path || "") + " " + (p.project.organization || "")).toLowerCase();
      if (q && !hay.includes(q)) continue;
      list.append(el("a", { href: "#/p/" + enc(p.key), class: "row" },
        el("span", { class: "title" }, p.global ? "Global memories" : p.key),
        el("span", { class: "badge" }, p.summary.total + " active")));
    }
    if (!list.children.length) list.append(el("p", { class: "muted" }, "No matching projects."));
  };
  box.addEventListener("input", draw);
  view.append(box, list);
  draw();
}

function tile(p, label, n, params) {
  const href = "#/p/" + enc(p.key) + "/list?" + new URLSearchParams(params).toString();
  return el("a", { href, class: "tile" + (n ? "" : " zero") }, el("span", { class: "n" }, String(n)), label);
}
function projectView(p) {
  setCrumbs(link("#/", "Projects"), el("span", {}, p.global ? "Global" : p.key));
  const info = el("dl");
  const pr = p.project;
  for (const [k, v] of [["Path", pr.local_path], ["Remote", pr.remote_url], ["Organization", pr.organization], ["Description", pr.description], ["Registered", pr.created_at]]) {
    if (v) info.append(el("dt", {}, k), el("dd", {}, v));
  }
  view.append(info);
  view.append(el("h2", {}, "Active memories: " + p.summary.total));
  const types = el("div", { class: "tiles" });
  for (const t of TYPES) types.append(tile(p, t, p.summary.by_type[t] || 0, { type: t, status: "active" }));
  view.append(types);
  view.append(el("h2", {}, "Handoffs"));
  const handoffs = el("div", { class: "tiles" });
  for (const h of HANDOFF) handoffs.append(tile(p, h.replace("_", " "), p.summary.by_handoff_status[h] || 0, { handoff: h, status: "active" }));
  view.append(handoffs);
  view.append(el("h2", {}, "By status"));
  const statuses = el("div", { class: "tiles" });
  for (const s of STATUSES) statuses.append(tile(p, s, p.summary.by_status[s] || 0, { status: s }));
  view.append(statuses);
  if (!data.include_inactive) view.append(el("p", { class: "note" }, "This snapshot holds active memories only. Superseded, archived and expired counts are shown, but their records are not included."));
  if (p.omitted) view.append(el("p", { class: "note" }, p.omitted + " memories were left out of this snapshot by its size limit."));
}

function listView(p, query) {
  const type = query.get("type") || "", status = query.get("status") || "", handoff = query.get("handoff") || "";
  const label = [handoff && "handoff " + handoff.replace("_", " "), type, status].filter(Boolean).join(" · ") || "all";
  setCrumbs(link("#/", "Projects"), link("#/p/" + enc(p.key), p.global ? "Global" : p.key), el("span", {}, label));
  const rows = p.memories.filter((m) =>
    (!type || m.type === type) && (!status || m.bucket === status) && (!handoff || m.handoff_status === handoff));
  if (!rows.length) view.append(el("p", { class: "muted" }, "No memories in this snapshot match."));
  for (const m of rows) {
    view.append(el("a", { href: "#/m/" + enc(m.id), class: "row" },
      el("span", { class: "title" }, m.title),
      el("span", { class: "badge" }, m.type + " · v" + m.version + " · " + m.updated_at.slice(0, 10))));
  }
}

function memoryView(m, p) {
  setCrumbs(link("#/", "Projects"), link("#/p/" + enc(p.key), p.global ? "Global" : p.key), el("span", {}, m.type));
  view.append(el("h2", {}, m.title));
  const meta = el("dl");
  const claim = " (unauthenticated claim)";
  for (const [k, v] of [
    ["ID", m.id], ["Type", m.type], ["Status", m.bucket], ["Version", String(m.version)],
    ["Updated", m.updated_at], ["Created", m.created_at], ["Tags", (m.tags || []).join(", ")],
    ["Source agent", m.source_agent && m.source_agent + claim], ["Evidence", m.evidence && m.evidence + claim],
    ["Source reference", m.source_reference], ["Last verified", m.last_verified_at], ["Expires", m.expires_at],
    ["Superseded by", m.superseded_by], ["Archive reason", m.archive_reason],
  ]) if (v) meta.append(el("dt", {}, k), el("dd", {}, v));
  view.append(meta);
  if (m.content_truncated) view.append(el("p", { class: "note" }, "Content is truncated in this snapshot (" + m.total_chars + " characters in total). Use memory_get for the full text."));
  if (m.handoff) handoffDetail(m.handoff);
  else view.append(el("h2", {}, "Content"), el("pre", {}, m.content));
}
function handoffDetail(h) {
  view.append(el("h2", {}, "Handoff " + (h.task_identifier || "")));
  const dl = el("dl");
  for (const [k, v] of [["Status", h.status], ["Summary", h.summary], ["Branch", h.branch], ["Commit", h.commit_reference]]) if (v) dl.append(el("dt", {}, k), el("dd", {}, v));
  view.append(dl);
  for (const [title, items] of [["Completed work", h.completed_work], ["Blockers", h.blockers], ["Next actions", h.next_actions], ["Committed changes", h.committed_changes], ["Uncommitted changes", h.uncommitted_changes]]) {
    if (!items || !items.length) continue;
    const ul = el("ul", { class: "plain" });
    for (const item of items) ul.append(el("li", {}, item));
    view.append(el("h2", {}, title), ul);
  }
  const vr = h.verification_results || {};
  if (Object.keys(vr).length) {
    const v = el("dl");
    for (const [k, val] of Object.entries(vr)) v.append(el("dt", {}, k), el("dd", {}, val));
    view.append(el("h2", {}, "Verification"), v);
  }
  if ((h.related_memory_ids || []).length) {
    const ul = el("ul", { class: "plain" });
    for (const id of h.related_memory_ids) {
      const hit = memoryById(id);
      ul.append(el("li", {}, hit ? link("#/m/" + enc(id), hit.m.title) : el("span", {}, id + " (not in this snapshot)")));
    }
    view.append(el("h2", {}, "Related memories"), ul);
  }
}

document.getElementById("meta").textContent =
  "Read-only snapshot generated " + data.generated_at + ". It does not update; stored text is untrusted data.";
if (data.truncated) crumbs.before(el("p", { class: "note" }, "Record limit reached: " + data.omitted_total + " memories are not in this snapshot."));
window.addEventListener("hashchange", route);
route();
