// How three columns share a window.
//
// Pure, and separate from the components, because the interesting behaviour is
// entirely in the arithmetic: what gives when the window is too narrow to
// satisfy everyone. The agent column is the one that must not vanish — it holds
// a terminal, and a terminal asked to be zero columns wide is not a small
// terminal, it is a reflow of the whole scrollback into nothing.

// Floors only. There is deliberately no maximum on either column: an upper
// bound would be a number invented here about how wide someone is allowed to
// make their own sidebar, and there is already a real constraint doing that
// job. A column can grow until the agent column reaches its floor and its
// neighbour reaches its own, and that is the whole of the limit.
export const SIDEBAR = { min: 160 } as const;
export const ACCESSORY = { min: 200 } as const;

// The agent column's floor, in pixels, and it is a floor rather than a
// preference. Below roughly this the pane is too narrow to read a wrapped
// agent transcript in, and the point of the layout is gone.
export const AGENT_MIN = 320;

/** Each divider is one pixel of layout; the grab area overhangs it. */
export const DIVIDER = 1;

export type Columns = {
  readonly sidebar: number;
  readonly accessory: number;
};

// fitColumns answers what the columns actually get, given what was asked for.
//
// The asked-for widths are kept separately by the caller and passed in each
// time, rather than being overwritten with the result. Narrowing the window
// squeezes a column; widening it back should return it to where its owner put
// it, and that is only possible if the request survives the squeeze.
export function fitColumns(container: number, want: Columns): Columns {
  let sidebar = Math.max(want.sidebar, SIDEBAR.min);
  let accessory = Math.max(want.accessory, ACCESSORY.min);

  // What the two of them may occupy before the agent column is starved.
  const budget = container - 2 * DIVIDER - AGENT_MIN;
  let over = sidebar + accessory - budget;

  // The accessory yields first. It is the one holding a diff or a webview,
  // which degrade gracefully; the sidebar is a list of names, which does not.
  if (over > 0) {
    const give = Math.min(over, accessory - ACCESSORY.min);
    accessory -= give;
    over -= give;
  }
  if (over > 0) {
    sidebar -= Math.min(over, sidebar - SIDEBAR.min);
  }

  // Both are at their floor and the window is still too narrow. The agent
  // column has already gone under its own floor by this point; what remains is
  // to keep the layout inside the window, because nothing here is allowed to
  // produce a top-level scrollbar. Scale both down together rather than
  // draining one: at this size neither is usable, and a lopsided result reads
  // as a bug rather than as a window nobody should be using.
  const room = Math.max(0, container - 2 * DIVIDER);
  if (sidebar + accessory > room) {
    const scale = room / (sidebar + accessory);
    sidebar = Math.floor(sidebar * scale);
    accessory = Math.floor(accessory * scale);
  }

  return { sidebar, accessory };
}
