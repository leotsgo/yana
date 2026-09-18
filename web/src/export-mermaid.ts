// The static export's diagram runtime: mermaid bundled as one classic
// script, so a page opened from file:// draws its fences with nothing to
// fetch. Exports carry only the light palette, so the theme is fixed.
import mermaid from 'mermaid'
import { drawMermaid } from './rich'
import type { MermaidLike } from './rich'

void drawMermaid(document, mermaid as unknown as MermaidLike, false)
