import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { basename, dirname, join, relative, resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const guildRuns = join(root, ".guild", "runs");
const outputDir = join(root, "research");
const markedVersion = "18.0.6";

function walk(directory) {
  const results = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) results.push(...walk(path));
    if (entry.isFile() && entry.name.endsWith(".md") && path.includes(`${join("", "research")}`)) {
      results.push(path);
    }
  }
  return results;
}

function escapeHtml(value) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function stripHtml(value) {
  return value.replace(/<[^>]*>/g, "").replace(/\s+/g, " ").trim();
}

function summarizeMarkdown(value, limit = 280) {
  const text = value
    .replace(/!\[([^\]]*)\]\([^)]+\)/g, "$1")
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    .replace(/[*_`~]/g, "")
    .replace(/\s+/g, " ")
    .trim();
  return text.length <= limit ? text : `${text.slice(0, limit - 1).trimEnd()}…`;
}

function stripFrontmatter(value) {
  return value.replace(/^---\s*\n[\s\S]*?\n---\s*\n/, "");
}

function nestedMarkdown(value) {
  return stripFrontmatter(value)
    .replace(/^#\s+.*\n+/, "")
    .replace(/^(#{2,5})\s/gm, (_, marks) => `${marks}# `);
}

function slugify(value, seen) {
  const raw = stripHtml(value)
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[^a-z0-9\s-]/g, "")
    .trim()
    .replace(/\s+/g, "-") || "section";
  const base = /^[a-z]/.test(raw) ? raw : `section-${raw}`;
  const count = seen.get(base) ?? 0;
  seen.set(base, count + 1);
  return count === 0 ? base : `${base}-${count + 1}`;
}

function addHeadingIds(html) {
  const seen = new Map();
  const headings = [];
  const output = html.replace(/<h([1-4])>([\s\S]*?)<\/h\1>/g, (_, level, content) => {
    const id = slugify(content, seen);
    const title = stripHtml(content);
    headings.push({ level: Number(level), id, title });
    return `<h${level} id="${id}">${content}<a class="heading-link" href="#${id}" aria-label="Link to ${escapeHtml(title)}">#</a></h${level}>`;
  });
  return { html: output, headings };
}

function renderToc(headings) {
  return headings
    .filter(({ level }) => level >= 2 && level <= 3)
    .map(({ level, id, title }) => `<a class="toc-link toc-level-${level}" href="#${id}">${escapeHtml(title)}</a>`)
    .join("\n");
}

function shell({ title, sources, updated, content, toc, kind = "Research brief" }) {
  const browserTitle = title.length > 54 ? `${title.slice(0, 51).trimEnd()}…` : title;
  const sourceLinks = sources
    .map(({ href, label }) => `<a class="source-link" href="${escapeHtml(href)}">${escapeHtml(label)}</a>`)
    .join("\n");
  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light dark">
  <meta name="description" content="${escapeHtml(title)}">
  <title>${escapeHtml(browserTitle)} · Amux Research</title>
  <link rel="stylesheet" href="assets/research.css">
</head>
<body>
  <div class="reading-progress" aria-hidden="true"><span></span></div>
  <header class="topbar">
    <a class="brand" href="index.html" aria-label="All Amux research">
      <span class="brand-mark">A</span>
      <span><strong>Amux</strong><small>Research library</small></span>
    </a>
    <div class="toolbar" role="group" aria-label="Reader controls">
      <label class="search-control"><span>Find</span><input id="page-search" type="search" placeholder="Search this report" autocomplete="off"></label>
      <button id="theme-toggle" type="button" aria-label="Toggle color theme">Theme</button>
      <button type="button" onclick="window.print()">Print</button>
    </div>
  </header>
  <div class="reader-layout">
    <aside class="toc" aria-label="Table of contents">
      <div class="toc-heading">On this page</div>
      <nav aria-label="Report sections">${toc}</nav>
      <div class="source-links" aria-label="Canonical source files">${sourceLinks}</div>
    </aside>
    <main id="main-content">
      <div class="report-meta">
        <span>${escapeHtml(kind)}</span>
        <span>Updated ${escapeHtml(updated)}</span>
      </div>
      <article class="research-article">${content}</article>
      <footer class="report-footer">
        <p>Generated from canonical Guild artifacts.</p>
        <a href="index.html">Back to all research</a>
      </footer>
    </main>
  </div>
  <button class="mobile-toc-button" type="button" aria-expanded="false" aria-controls="mobile-toc">Contents</button>
  <div id="mobile-toc" class="mobile-toc" hidden><nav aria-label="Mobile report sections">${toc}</nav></div>
  <script src="assets/research.js"></script>
</body>
</html>`;
}

mkdirSync(join(outputDir, "assets"), { recursive: true });
const files = walk(guildRuns).sort();
const reports = [];

for (const file of files) {
  const markdown = readFileSync(file, "utf8");
  const title = markdown.match(/^#\s+(.+)$/m)?.[1]?.trim() ?? basename(file, ".md");
  const rendered = execFileSync("npx", ["--yes", `marked@${markedVersion}`, "--gfm"], {
    cwd: root,
    input: markdown,
    encoding: "utf8",
    maxBuffer: 16 * 1024 * 1024,
  });
  const withIds = addHeadingIds(rendered);
  const filename = `${basename(file, ".md")}.html`;
  const updated = new Intl.DateTimeFormat("en", { dateStyle: "medium" }).format(statSync(file).mtime);
  const source = relative(outputDir, file);
  const description = markdown.match(/## TL;DR\s+\n+[-*]\s+(.+)/)?.[1] ?? "Repository research and implementation guidance.";
  writeFileSync(join(outputDir, filename), shell({
    title,
    sources: [{ href: source, label: "View canonical Markdown" }],
    updated,
    content: withIds.html,
    toc: renderToc(withIds.headings),
  }));
  reports.push({ title, filename, updated, description, kind: "Research brief" });
}

const planningFiles = [
  { label: "Approved specification", path: join(root, ".guild", "spec", "amux-go-linux-runtime.md") },
  { label: "Product requirements", path: join(root, ".guild", "prd", "amux-go-linux-runtime.md") },
  { label: "Implementation plan", path: join(root, ".guild", "plan", "amux-go-linux-runtime.md") },
];

if (planningFiles.every(({ path }) => existsSync(path))) {
  const title = "Amux Go implementation specification and plan";
  const markdown = [
    `# ${title}`,
    "",
    "> Review bundle generated from the approved Guild specification and the pending PRD/implementation plan. The linked source files remain authoritative.",
    "",
    ...planningFiles.flatMap(({ label, path }) => [
      `## ${label}`,
      "",
      nestedMarkdown(readFileSync(path, "utf8")),
      "",
    ]),
  ].join("\n");
  const rendered = execFileSync("npx", ["--yes", `marked@${markedVersion}`, "--gfm"], {
    cwd: root,
    input: markdown,
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
  });
  const withIds = addHeadingIds(rendered);
  const filename = "amux-go-implementation-plan.html";
  const latestMtime = Math.max(...planningFiles.map(({ path }) => statSync(path).mtimeMs));
  const updated = new Intl.DateTimeFormat("en", { dateStyle: "medium" }).format(new Date(latestMtime));
  writeFileSync(join(outputDir, filename), shell({
    title,
    sources: planningFiles.map(({ label, path }) => ({
      href: relative(outputDir, path),
      label,
    })),
    updated,
    content: withIds.html,
    toc: renderToc(withIds.headings),
    kind: "Planning review",
  }));
  reports.push({
    title,
    filename,
    updated,
    kind: "Planning review",
    description: "Approved Linux-first Go product specification, tool choices, implementation waves, six specialist lanes, dependency gates, and measurable release evidence.",
  });
}

const list = reports.map((report, index) => `
  <article class="library-entry">
    <span class="entry-number">${String(index + 1).padStart(2, "0")}</span>
    <div>
      <p class="entry-meta">${escapeHtml(report.kind)} · Updated ${escapeHtml(report.updated)}</p>
      <h2><a href="${report.filename}">${escapeHtml(report.title)}</a></h2>
      <p>${escapeHtml(summarizeMarkdown(report.description))}</p>
    </div>
    <a class="entry-action" href="${report.filename}" aria-label="Read ${escapeHtml(report.title)}">Read report</a>
  </article>`).join("\n");

writeFileSync(join(outputDir, "index.html"), `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light dark">
  <title>Amux Research Library</title>
  <link rel="stylesheet" href="assets/research.css">
</head>
<body class="library-page">
  <header class="topbar">
    <a class="brand" href="index.html"><span class="brand-mark">A</span><span><strong>Amux</strong><small>Research library</small></span></a>
    <div class="toolbar"><button id="theme-toggle" type="button" aria-label="Toggle color theme">Theme</button></div>
  </header>
  <main class="library-main">
    <div class="library-intro">
      <p class="eyebrow">Evidence before implementation</p>
      <h1>Research library</h1>
      <p>Durable repository investigations, feature maps, and architecture recommendations. Markdown remains canonical; these pages are optimized for review.</p>
      <dl><div><dt>Reports</dt><dd>${reports.length}</dd></div><div><dt>Source</dt><dd>Guild research</dd></div><div><dt>Mode</dt><dd>Local, static HTML</dd></div></dl>
    </div>
    <section class="library-list" aria-label="Research reports">${list}</section>
  </main>
  <script src="assets/research.js"></script>
</body>
</html>`);

console.log(`Rendered ${reports.length} research page${reports.length === 1 ? "" : "s"} into ${relative(root, outputDir)}/`);
