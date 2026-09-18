// Diagrams and math in rendered notes. The server marks a ```mermaid
// fence as <pre class="mermaid"> and TeX as .math spans and divs, each
// holding the escaped source; this module draws them in place. It takes
// the libraries as arguments so the app can load them lazily (rich-load)
// and the exports can bundle them into one classic script (export-mermaid,
// export-katex) — the same code runs in both.

/** The slice of mermaid this module uses. */
export interface MermaidLike {
  initialize(config: Record<string, unknown>): void
  render(id: string, text: string): Promise<{ svg: string }>
}

/** The slice of KaTeX this module uses. */
export interface KatexLike {
  render(tex: string, el: HTMLElement, options: Record<string, unknown>): void
}

export function hasMermaid(root: ParentNode): boolean {
  return root.querySelector('pre.mermaid') !== null
}

export function hasMath(root: ParentNode): boolean {
  return root.querySelector('.math') !== null
}

let seq = 0
// Drawn diagrams keyed by theme and source: the live preview re-renders
// the whole note every few hundred milliseconds while typing, and an
// unchanged diagram should not be laid out again.
const drawn = new Map<string, string>()

/**
 * Draws every undrawn mermaid fence under root. A diagram that fails to
 * parse keeps its source and gains a one-line message; the rest of the
 * page is unaffected. dark picks the theme; the colours come from the
 * page's own custom properties, so a diagram follows the app's palette.
 */
export async function drawMermaid(root: ParentNode, mermaid: MermaidLike, dark: boolean): Promise<void> {
  const pres = Array.from(root.querySelectorAll<HTMLPreElement>('pre.mermaid'))
  if (pres.length === 0) return
  const style = getComputedStyle(document.documentElement)
  const v = (name: string, fallback: string) => style.getPropertyValue(name).trim() || fallback
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: 'strict',
    suppressErrorRendering: true,
    theme: 'base',
    fontFamily: v('--sans', 'sans-serif'),
    themeVariables: {
      darkMode: dark,
      background: v('--bg', dark ? '#1c1a17' : '#faf7f2'),
      primaryColor: v('--accent-soft', dark ? '#3a2914' : '#f6e6d2'),
      primaryTextColor: v('--ink', dark ? '#ece7dd' : '#1d1b18'),
      primaryBorderColor: v('--accent', '#b8691e'),
      secondaryColor: v('--bg-3', dark ? '#2c2821' : '#ebe6dc'),
      secondaryTextColor: v('--ink', dark ? '#ece7dd' : '#1d1b18'),
      secondaryBorderColor: v('--line-2', dark ? '#433e35' : '#d3ccbe'),
      tertiaryColor: v('--bg-2', dark ? '#232019' : '#f3efe7'),
      tertiaryTextColor: v('--ink', dark ? '#ece7dd' : '#1d1b18'),
      tertiaryBorderColor: v('--line', dark ? '#322e27' : '#e2dcd0'),
      lineColor: v('--ink-2', dark ? '#b4ac9e' : '#5b564e'),
      textColor: v('--ink', dark ? '#ece7dd' : '#1d1b18'),
      noteBkgColor: v('--accent-soft', dark ? '#3a2914' : '#f6e6d2'),
      noteTextColor: v('--ink', dark ? '#ece7dd' : '#1d1b18'),
      noteBorderColor: v('--accent', '#b8691e'),
      fontFamily: v('--sans', 'sans-serif'),
      fontSize: '14px',
    },
  })
  for (const pre of pres) {
    const src = pre.textContent ?? ''
    const figure = document.createElement('figure')
    figure.className = 'mermaid-figure'
    figure.dataset['src'] = src
    const key = (dark ? 'd:' : 'l:') + src
    try {
      let svg = drawn.get(key)
      if (svg === undefined) {
        svg = (await mermaid.render('yana-mermaid-' + ++seq, src)).svg
        drawn.set(key, svg)
      }
      figure.innerHTML = svg
    } catch (err) {
      figure.className += ' mermaid-error'
      const message = document.createElement('p')
      message.className = 'mermaid-message'
      message.textContent = firstLine(err)
      const source = document.createElement('pre')
      source.textContent = src
      figure.append(message, source)
    }
    pre.replaceWith(figure)
  }
}

/** Puts every drawn diagram under root back to its source, to redraw. */
export function undrawMermaid(root: ParentNode): void {
  for (const figure of root.querySelectorAll<HTMLElement>('figure.mermaid-figure')) {
    const pre = document.createElement('pre')
    pre.className = 'mermaid'
    pre.textContent = figure.dataset['src'] ?? ''
    figure.replaceWith(pre)
  }
}

/**
 * Typesets every .math span and div under root in place. Bad TeX is
 * shown as the source in the danger colour; nothing throws.
 */
export function typeset(root: ParentNode, katex: KatexLike): void {
  const errorColor = getComputedStyle(document.documentElement).getPropertyValue('--danger').trim() || '#9a3412'
  for (const el of root.querySelectorAll<HTMLElement>('.math:not(.math-done)')) {
    const tex = el.textContent ?? ''
    katex.render(tex, el, {
      displayMode: el.classList.contains('math-display'),
      throwOnError: false,
      errorColor,
    })
    el.classList.add('math-done')
  }
}

// One line from mermaid's error: its first line names the line number
// ("Parse error on line 2:") and a later one says what was expected;
// the marked-up source between them is already shown below.
function firstLine(err: unknown): string {
  const text = err instanceof Error ? err.message : String(err)
  const lines = text.split('\n').filter((l) => l.trim() !== '')
  const head = lines[0] ?? 'The diagram could not be drawn.'
  const detail = lines.slice(1).find((l) => /^Expecting/.test(l))
  const line = detail ? head.replace(/:\s*$/, '') + ': ' + detail : head
  return line.length > 200 ? line.slice(0, 200) + '…' : line
}
