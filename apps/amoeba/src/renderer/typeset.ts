import * as stylex from "@stylexjs/stylex";
import { text } from "./tokens.stylex";

// The type roles, which is what the scale turned out to need.
//
// ── the scale is not what was being used ─────────────────────────────────
//
// Counted across the renderer before this file existed: 168 style entries set
// a type property, in 21 combinations, and one size did nearly all the work.
//
//   text.small   135 of 156 sized entries   87%
//   text.body     11
//   text.lead      6
//   text.title     2
//
// So naming *sizes* semantically would have renamed four things, three of
// which appear twice. What actually repeats is a **pair** — a size with a
// family, or a size with a weight:
//
//   small + mono   28   a slug, a path, a revision, a command, a hex
//   small + ui     15   a hint, a state word, a caption
//   body  + ui      7   a container children read in
//   small + medium  6   a tab, a button, a send
//   small + strong  3   a section heading inside a panel
//   lead  + medium  5   a panel or dialog title
//
// That is 64 entries in six shapes, and each shape is a *role*: the same
// decision, restated by hand, once per panel. This file is the decision.
//
// ── they compose at the call site, and that is StyleX's doing ────────────
//
// `stylex.create` cannot include another entry — there is no `include` in
// 0.19 — so a role is applied where a style is applied:
//
//   {...stylex.props(typeset.address, styles.slug)}
//
// which is why `styles.slug` still exists: it holds the colour and the
// truncation, and the role holds the face and the size. The alternative was a
// token per property, which is what `tokens.stylex.ts` already is and is
// exactly what left 64 hand-written pairs to drift.
//
// ── one 16px weight, not two ─────────────────────────────────────────────
//
// `lead + medium` and `lead + strong` were both in use for the same thing: a
// title over a panel (Sidebar, Pr, Mcp) and a title over a dialog
// (ArchiveThread, the style guide). There is no distinction worth two roles
// there, so `heading` is `medium` — the majority, and what the type specimen
// on the style guide already advertises. Two dialog titles lose 100 of weight,
// which is the visible half of this change and the reason it is written down.

export const typeset = stylex.create({
  /**
   * A container whose children are read rather than glanced at.
   *
   * Set on the box and inherited: the window itself, a dialog, a message, the
   * composer's own textarea. The only role that is usually applied to
   * something with no text of its own.
   */
  prose: { fontFamily: text.ui, fontSize: text.body },

  /** A panel's or a dialog's own title. */
  heading: { fontFamily: text.ui, fontSize: text.lead, fontWeight: text.medium },

  /**
   * A heading *inside* a panel — an inbox section, a thread's name.
   *
   * Caption size and heavy, which is the window's way of separating hierarchy
   * without another size: the floor is 14px and there is nothing below it to
   * spend, so weight does the work.
   */
  subhead: { fontFamily: text.ui, fontSize: text.small, fontWeight: text.strong },

  /** Something you press: a tab, a button, the send. */
  control: { fontFamily: text.ui, fontSize: text.small, fontWeight: text.medium },

  /** A caption, a hint, a state word — prose at the smallest size. */
  label: { fontFamily: text.ui, fontSize: text.small },

  /**
   * A thing somebody will type somewhere else.
   *
   * A slug, a bookmark, a revision, a path, a command, a hex. The line this
   * window draws between the two families is address versus prose, and this is
   * the address side of it — 28 entries wrote the pair out by hand before this
   * role existed, which is 28 chances for one of them to be a `fontFamily`
   * with no `fontSize`.
   */
  address: { fontFamily: text.mono, fontSize: text.small },
});
