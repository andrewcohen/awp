# patches

`ghostty-web-renderer.patch` fixes three defects in ghostty-web's canvas
renderer. All three are bugs rather than preferences, all three are small, and
the intent is to send them upstream and delete this directory.

Apply against `frontend/node_modules/ghostty-web/dist/ghostty-web.js`. It is a
built bundle, not source, so the patch is written against the shipped artifact —
which is also why it will need regenerating whenever the dependency moves.

**Vite pre-bundles dependencies and keys that cache on the lockfile, not on file
contents.** Editing `node_modules` therefore changes nothing until
`frontend/node_modules/.vite` is deleted. Two separate conclusions were drawn
from screenshots of an unpatched build before this was noticed; delete the cache
and confirm a marker (`strokeText`, `fontBoundingBoxAscent`) reaches
`.vite/deps/ghostty-web.js` before believing a result.

## What it changes

**Row height comes from the font, not from the ink of one glyph.** `measureFont`
measured `actualBoundingBoxAscent/Descent` of `"M"` — a letter with no descender
— so an 18px font got a 17px row, a line-height of 0.94, and descenders were
sliced off by the next row's background fill. `fontBoundingBoxAscent/Descent` is
what the face declares its line box to be: 18 + 5 for Maple Mono, so 23px. The
unconditional `+2` leading goes with it, since it existed to paper over the same
gap.

**A glyph is confined to its cell.** Each cell's background is filled and the
glyph drawn, with no clip and no `maxWidth`, so anything wider than the cell
painted over its neighbour — and because only dirty rows are repainted, the
spill stayed on screen. Bold text and fallback glyphs (box drawing, Nerd Font
icons, emoji) are routinely wider, since the cell was measured from regular `M`.
`fillText`'s fourth argument condenses an overwide glyph instead.

**Glyphs are thickened.** Core Text dilates stems for native text and Canvas2D
does not, so canvas glyphs read thin and washed out against every other app on
the screen — this is what Ghostty's `font-thicken` compensates for, and it has no
Canvas2D equivalent. A second `fillText` offset by a third of a pixel darkens the
stem.

The thickening was first written as a `strokeText` pass, which is the obvious
spelling and cost **p50 734ms** per keystroke in WKWebView against a 3ms
baseline: stroking rasterises the glyph outline as a path. A second fill is the
cheapest operation the canvas has and looks the same.

## What it deliberately does not change

**Cell width still rounds up.** `Math.ceil(measureText("M").width)` in CSS pixels
wastes up to a pixel per cell, and the honest fix is to measure in device pixels
— but narrowing the cell would condense every glyph through the `maxWidth` added
above, which is worse than a slightly loose column. Choosing a font size whose
advance nearly fills its cell is the cheap mitigation: Maple Mono at 16px wastes
0.1px against 15px's 0.73px.

**Dirty-row tracking is untouched.** A row the map fails to flag keeps stale
content, which is visible as a band of old text that never repaints. Forcing a
full repaint every frame does fix it, and that is what cost 734ms alongside the
stroking — a fix that makes typing unusable is not one. The real repair is in the
dirty tracking, which lives on the Zig side.
