// Colour themes. A theme is a set of the stylesheet's colour tokens for one
// mode, light or dark; applying it stamps them on <html> as inline custom
// properties over the stylesheet's own. Yana's own two themes are the
// stylesheet itself, so they carry no colours. A theme may give only bg, ink
// and accent: the rest are mixed from those (see themeVars).

export type Mode = 'light' | 'dark'

/** The tokens a theme may set, without the leading "--". */
export const TOKENS = [
  'bg', 'bg-2', 'bg-3', 'ink', 'ink-2', 'ink-3', 'line', 'line-2',
  'accent', 'accent-ink', 'accent-soft', 'accent-text', 'mark',
  'danger', 'danger-soft', 'ok', 'code-kw', 'code-str', 'code-fn', 'code-num',
] as const

export type Token = (typeof TOKENS)[number]

export interface ThemeDef {
  id: string
  name: string
  mode: Mode
  colors: Partial<Record<Token, string>>
}

// Short form for the built-ins, in TOKENS' order up to code-num, skipping
// the derived ones: bg bg-2 bg-3 ink ink-2 ink-3 line line-2 accent danger
// ok code-kw code-str code-fn code-num.
function t(id: string, name: string, mode: Mode, c: string): ThemeDef {
  const [bg, bg2, bg3, ink, ink2, ink3, line, line2, accent, danger, ok, kw, str, fn, num] = c.split(' ')
  return {
    id, name, mode,
    colors: {
      bg, 'bg-2': bg2, 'bg-3': bg3, ink, 'ink-2': ink2, 'ink-3': ink3, line, 'line-2': line2,
      accent, danger, ok, 'code-kw': kw, 'code-str': str, 'code-fn': fn, 'code-num': num,
    },
  }
}

export const YANA_LIGHT: ThemeDef = { id: 'yana-light', name: 'Yana', mode: 'light', colors: {} }
export const YANA_DARK: ThemeDef = { id: 'yana-dark', name: 'Yana', mode: 'dark', colors: {} }

export const BUILTIN: ThemeDef[] = [
  YANA_LIGHT,
  t('catppuccin-latte', 'Catppuccin Latte', 'light', '#eff1f5 #e6e9ef #dce0e8 #4c4f69 #6c6f85 #9ca0b0 #ccd0da #bcc0cc #8839ef #d20f39 #40a02b #8839ef #40a02b #1e66f5 #fe640b'),
  t('gruvbox-light', 'Gruvbox Light', 'light', '#fbf1c7 #f2e5bc #ebdbb2 #3c3836 #665c54 #928374 #ebdbb2 #d5c4a1 #af3a03 #9d0006 #79740e #9d0006 #79740e #076678 #8f3f71'),
  t('rose-pine-dawn', 'Rosé Pine Dawn', 'light', '#faf4ed #f2e9e1 #e9dfd6 #575279 #797593 #9893a5 #dfdad9 #cecacd #d7827e #b4637a #56949f #907aa9 #ea9d34 #286983 #b4637a'),
  t('solarized-light', 'Solarized Light', 'light', '#fdf6e3 #eee8d5 #e4ddc8 #586e75 #657b83 #93a1a1 #e4ddc8 #d6cfb9 #268bd2 #dc322f #859900 #859900 #2aa198 #268bd2 #d33682'),
  t('tokyo-night-day', 'Tokyo Night Day', 'light', '#e1e2e7 #d0d5e3 #c4c8da #3760bf #6172b0 #848cb5 #c4c8da #a8aecb #2e7de9 #f52a65 #587539 #9854f1 #587539 #2e7de9 #b15c00'),
  YANA_DARK,
  t('catppuccin-frappe', 'Catppuccin Frappé', 'dark', '#303446 #292c3c #232634 #c6d0f5 #a5adce #737994 #414559 #51576d #ca9ee6 #e78284 #a6d189 #ca9ee6 #a6d189 #8caaee #ef9f76'),
  t('catppuccin-macchiato', 'Catppuccin Macchiato', 'dark', '#24273a #1e2030 #181926 #cad3f5 #a5adcb #6e738d #363a4f #494d64 #c6a0f6 #ed8796 #a6da95 #c6a0f6 #a6da95 #8aadf4 #f5a97f'),
  t('catppuccin-mocha', 'Catppuccin Mocha', 'dark', '#1e1e2e #181825 #11111b #cdd6f4 #a6adc8 #6c7086 #313244 #45475a #cba6f7 #f38ba8 #a6e3a1 #cba6f7 #a6e3a1 #89b4fa #fab387'),
  t('dracula', 'Dracula', 'dark', '#282a36 #21222c #343746 #f8f8f2 #c3c6d4 #6272a4 #44475a #565a73 #bd93f9 #ff5555 #50fa7b #ff79c6 #f1fa8c #50fa7b #bd93f9'),
  t('everforest-dark', 'Everforest Dark', 'dark', '#2d353b #343f44 #3d484d #d3c6aa #9da9a0 #7a8478 #3d484d #475258 #a7c080 #e67e80 #a7c080 #e67e80 #dbbc7f #7fbbb3 #d699b6'),
  t('gruvbox-dark', 'Gruvbox Dark', 'dark', '#282828 #32302f #3c3836 #ebdbb2 #bdae93 #928374 #3c3836 #504945 #fe8019 #fb4934 #b8bb26 #fb4934 #b8bb26 #83a598 #d3869b'),
  t('kanagawa', 'Kanagawa', 'dark', '#1f1f28 #2a2a37 #363646 #dcd7ba #c8c093 #727169 #363646 #54546d #7e9cd8 #e46876 #98bb6c #957fb8 #98bb6c #7e9cd8 #d27e99'),
  t('nord', 'Nord', 'dark', '#2e3440 #3b4252 #434c5e #eceff4 #d8dee9 #7b88a1 #3b4252 #4c566a #88c0d0 #bf616a #a3be8c #81a1c1 #a3be8c #88c0d0 #b48ead'),
  t('one-dark', 'One Dark', 'dark', '#282c34 #21252b #2c313a #abb2bf #9da5b4 #5c6370 #3e4451 #4b5263 #61afef #e06c75 #98c379 #c678dd #98c379 #61afef #d19a66'),
  t('rose-pine', 'Rosé Pine', 'dark', '#191724 #1f1d2e #26233a #e0def4 #908caa #6e6a86 #26233a #403d52 #ebbcba #eb6f92 #9ccfd8 #c4a7e7 #f6c177 #ebbcba #9ccfd8'),
  t('solarized-dark', 'Solarized Dark', 'dark', '#002b36 #073642 #0d3f4c #93a1a1 #839496 #586e75 #073642 #194753 #268bd2 #dc322f #859900 #859900 #2aa198 #268bd2 #d33682'),
  t('tokyo-night', 'Tokyo Night', 'dark', '#1a1b26 #16161e #24283b #c0caf5 #a9b1d6 #565f89 #292e42 #3b4261 #7aa2f7 #f7768e #9ece6a #bb9af7 #9ece6a #7aa2f7 #ff9e64'),
]

/** Background, text and accent for a theme's swatch in settings. */
export function swatch(def: ThemeDef): [string, string, string] {
  const c = def.colors
  if (c.bg && c.ink && c.accent) return [c.bg, c.ink, c.accent]
  return def.mode === 'dark' ? ['#1c1a17', '#ece7dd', '#d9893b'] : ['#faf7f2', '#1d1b18', '#b8691e']
}

const HEX = /^#([0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/i

function rgb(hex: string): number[] {
  let h = hex.slice(1)
  if (h.length === 3) h = [...h].map((c) => c + c).join('')
  return [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16))
}

/** a moved t of the way towards b, as #rrggbb. */
function mix(a: string, b: string, t: number): string {
  const x = rgb(a), y = rgb(b)
  return '#' + x.map((v, i) => Math.round(v + (y[i]! - v) * t).toString(16).padStart(2, '0')).join('')
}

/** Every token a theme stamps on the root; empty for Yana's own. */
export function themeVars(def: ThemeDef): Partial<Record<Token | 'hover', string>> {
  const c = def.colors
  if (!c.bg || !c.ink || !c.accent) return {}
  const { bg, ink, accent } = c
  const dark = def.mode === 'dark'
  const danger = c.danger ?? (dark ? '#e0805e' : '#9a3412')
  const ok = c.ok ?? (dark ? '#8fbf7a' : '#3d6b2e')
  const [r, g, b] = rgb(ink)
  return {
    bg, ink, accent, danger, ok,
    'bg-2': c['bg-2'] ?? mix(bg, ink, 0.04),
    'bg-3': c['bg-3'] ?? mix(bg, ink, 0.08),
    'ink-2': c['ink-2'] ?? mix(ink, bg, 0.3),
    'ink-3': c['ink-3'] ?? mix(ink, bg, 0.5),
    line: c.line ?? mix(bg, ink, 0.1),
    'line-2': c['line-2'] ?? mix(bg, ink, 0.17),
    hover: `rgba(${r}, ${g}, ${b}, ${dark ? 0.06 : 0.045})`,
    'accent-ink': c['accent-ink'] ?? mix(accent, ink, 0.15),
    'accent-soft': c['accent-soft'] ?? mix(bg, accent, dark ? 0.2 : 0.15),
    'accent-text': c['accent-text'] ?? bg,
    mark: c.mark ?? mix(bg, accent, dark ? 0.32 : 0.25),
    'danger-soft': c['danger-soft'] ?? mix(bg, danger, 0.15),
    'code-kw': c['code-kw'] ?? accent,
    'code-str': c['code-str'] ?? ok,
    'code-fn': c['code-fn'] ?? mix(ink, accent, 0.5),
    'code-num': c['code-num'] ?? danger,
  }
}

/**
 * Reads a pasted theme: JSON with a name, a mode and colours as hex. Only
 * the known tokens are kept, and only hex values, so nothing but a colour
 * reaches the stylesheet.
 */
export function parseTheme(text: string): ThemeDef | string {
  let v: unknown
  try {
    v = JSON.parse(text)
  } catch {
    return 'That is not JSON. Copy a theme to see the shape.'
  }
  const o = v as { name?: unknown; mode?: unknown; colors?: unknown }
  if (!o || typeof o !== 'object') return 'A theme is a JSON object.'
  const name = typeof o.name === 'string' ? o.name.trim() : ''
  if (!name || name.length > 40) return 'Give the theme a name of up to 40 characters.'
  if (o.mode !== 'light' && o.mode !== 'dark') return 'Set "mode" to "light" or "dark".'
  if (!o.colors || typeof o.colors !== 'object') return 'Add "colors" with at least bg, ink and accent.'
  const colors: ThemeDef['colors'] = {}
  for (const [k, val] of Object.entries(o.colors)) {
    if (!(TOKENS as readonly string[]).includes(k)) return `"${k}" is not a theme colour.`
    if (typeof val !== 'string' || !HEX.test(val)) return `"${k}" needs a hex colour like #1e1e2e.`
    colors[k as Token] = val
  }
  if (!colors.bg || !colors.ink || !colors.accent) return 'A theme needs at least bg, ink and accent.'
  const slug = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
  return { id: 'custom:' + (slug || 'theme'), name, mode: o.mode, colors }
}

/** The theme on screen now as pasteable JSON, every token resolved. */
export function exportTheme(def: ThemeDef, root: Element): string {
  const css = getComputedStyle(root)
  const colors: Record<string, string> = {}
  for (const k of TOKENS) {
    const v = css.getPropertyValue('--' + k).trim()
    if (HEX.test(v)) colors[k] = v
  }
  return JSON.stringify({ name: def.name + ' (copy)', mode: def.mode, colors }, null, 2)
}
