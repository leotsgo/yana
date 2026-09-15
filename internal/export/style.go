// The stylesheet every export carries. Site exports link it once from
// each page; the single-file export inlines it. The palette and type
// choices match the app: warm off-white, near-black ink, one amber
// accent, humanist sans for the page and mono for the wordmark and code.
package export

const siteCSS = `
:root {
  --bg: #faf7f2; --bg-2: #f3efe7; --bg-3: #ebe6dc;
  --ink: #1d1b18; --ink-2: #5b564e; --ink-3: #8d877c;
  --line: #ddd5c7; --accent: #b8691e; --accent-soft: #f6e6d2;
  --sans: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", sans-serif;
  --mono: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
* { box-sizing: border-box; }
html { -webkit-text-size-adjust: 100%; }
body {
  margin: 0; font: 16px/1.6 var(--sans); color: var(--ink); background: var(--bg);
}
a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }
.wordmark {
  font-family: var(--mono); font-weight: 600; font-size: 16px;
  letter-spacing: 0.02em; color: var(--ink);
}
.wordmark:hover { color: var(--accent); text-decoration: none; }

/* layout */
.x-layout { display: grid; grid-template-columns: 280px minmax(0, 1fr); min-height: 100vh; }
.x-nav {
  border-right: 1px solid var(--line); background: var(--bg-2);
  padding: 20px 14px; font-size: 14px; overflow-wrap: anywhere;
}
.x-nav-head { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; margin-bottom: 12px; }
.x-search-link { font-size: 13px; color: var(--ink-2); }
.x-main { padding: 28px 34px 60px; max-width: 52rem; }
.x-path {
  font-family: var(--mono); font-size: 12px; color: var(--ink-3);
  margin: 0 0 18px; overflow-wrap: anywhere;
}
@media (max-width: 760px) {
  .x-layout { display: block; }
  .x-nav { border-right: 0; border-bottom: 1px solid var(--line); }
  .x-main { padding: 20px 16px 40px; }
}

/* nav tree */
.x-tree .x-dirname {
  display: block; margin: 10px 0 2px; font-size: 12px; text-transform: none;
  letter-spacing: 0.04em; color: var(--ink-2); overflow-wrap: anywhere;
}
.x-tree a {
  display: block; padding: 2px 6px; color: var(--ink); white-space: nowrap;
  overflow: hidden; text-overflow: ellipsis; border-radius: 3px;
}
.x-tree a:hover { background: var(--bg-3); text-decoration: none; }
.x-tree a.current { background: var(--accent-soft); color: var(--ink); font-weight: 600; }

/* note typography */
.x-note { overflow-wrap: break-word; }
.x-note h1, .x-note h2, .x-note h3, .x-note h4 { line-height: 1.25; margin: 1.4em 0 0.5em; }
.x-note h1 { font-size: 26px; margin-top: 0; }
.x-note h2 { font-size: 21px; }
.x-note h3 { font-size: 18px; }
.x-note p { margin: 0.6em 0; }
.x-note img { max-width: 100%; height: auto; }
.x-note code { font: 0.9em var(--mono); background: var(--bg-3); padding: 1px 4px; border-radius: 2px; }
.x-note pre {
  background: var(--bg-3); border: 1px solid var(--line); border-radius: 4px;
  padding: 10px 12px; overflow-x: auto;
}
.x-note pre code { background: none; padding: 0; font-size: inherit; }
.x-note blockquote {
  margin: 0.8em 0; padding: 2px 14px; border-left: 3px solid var(--accent-soft);
  color: var(--ink-2);
}
.x-note table { border-collapse: collapse; margin: 1em 0; font-size: 14px; }
.x-note th, .x-note td { border: 1px solid var(--line); padding: 5px 10px; text-align: left; }
.x-note th { background: var(--bg-2); }
.x-note hr { border: 0; border-top: 1px solid var(--line); margin: 1.6em 0; }
.x-note .footnotes { font-size: 14px; color: var(--ink-2); }

/* chroma highlighting (classes only; warm, low-contrast) */
.chroma .k, .chroma .kd, .chroma .kn, .chroma .kr, .chroma .kt { color: #8a3d0f; }
.chroma .s, .chroma .s1, .chroma .s2, .chroma .sb { color: #5f6f2a; }
.chroma .c, .chroma .c1, .chroma .cm { color: var(--ink-3); font-style: italic; }
.chroma .nf, .chroma .nx { color: #1f4e79; }
.chroma .m, .chroma .mi, .chroma .mf { color: #7a4d9a; }

/* links */
a.wikilink { color: var(--accent); }
.wikilink.unresolved {
  color: var(--ink-3); border-bottom: 1px dotted var(--ink-3); cursor: default;
}

/* backlinks */
.x-backlinks {
  margin-top: 3em; padding-top: 1em; border-top: 1px solid var(--line); font-size: 14px;
}
.x-backlinks h2 { font-size: 15px; color: var(--ink-2); margin: 0 0 0.5em; }
.x-backlinks ul { list-style: none; margin: 0; padding: 0; }
.x-backlinks li { margin: 0 0 10px; }
.x-backlinks .x-context { margin: 2px 0 0; color: var(--ink-2); font-size: 13px; }

/* home and search pages */
.x-home-list { list-style: none; margin: 1em 0; padding: 0; }
.x-home-list li { margin: 0 0 8px; }
.x-home-list .x-note-path { display: block; font-family: var(--mono); font-size: 12px; color: var(--ink-3); }
.x-searchbox {
  width: 100%; max-width: 30rem; font: inherit; color: var(--ink); background: var(--bg);
  border: 1px solid var(--line); border-radius: 4px; padding: 8px 10px; margin: 1em 0;
}
.x-searchbox:focus { outline: 2px solid var(--accent-soft); border-color: var(--accent); }
.x-results { margin-top: 1em; }
.x-result { display: block; padding: 8px 10px; border-radius: 4px; }
.x-result:hover { background: var(--bg-2); text-decoration: none; }
.x-result .t { color: var(--ink); font-weight: 600; }
.x-result .p { display: block; font-family: var(--mono); font-size: 12px; color: var(--ink-3); }
.x-muted { color: var(--ink-3); }

/* single-file export */
.export-single { max-width: 46rem; margin: 0 auto; padding: 24px 20px 60px; }
.export-single .x-header {
  display: flex; justify-content: space-between; gap: 12px; align-items: baseline;
  border-bottom: 1px solid var(--line); padding-bottom: 10px; margin-bottom: 24px;
}
.export-single .x-meta { font-size: 12px; color: var(--ink-3); font-family: var(--mono); }
`
