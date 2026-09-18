// The static export's math runtime: KaTeX bundled as one classic script;
// its stylesheet and fonts sit beside it (or inside the single-file
// export as data URIs).
import katex from 'katex'
import { typeset } from './rich'
import type { KatexLike } from './rich'

typeset(document, katex as unknown as KatexLike)
