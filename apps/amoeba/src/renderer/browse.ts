// Turning what a person typed into somewhere to go.
//
// Its own file, and not because Web.tsx is long. A component file imports
// `tokens.stylex`, and `stylex.defineConsts` cannot be evaluated outside the
// Babel pass — so a test that imports one dies on the tokens before it reaches
// the function. The same shape as the barrel that dragged `node:fs` into the
// renderer: what a module *reaches* is part of its interface.

/**
 * What a person typed, as something that can be navigated to.
 *
 * Three cases, and the middle one is the reason this is not a one-liner:
 *
 *   https://example.com   already an address, left alone
 *   localhost:5173        a host and a port — http, because nothing local has
 *                         a certificate and https would fail to connect
 *   effect schema v4      not an address at all — a search
 *
 * The port test is what separates the second from the third. `foo:bar` with a
 * non-numeric part after the colon is prose far more often than it is a URL
 * scheme, and treating it as one produces a navigation to nowhere.
 */
export const addressFor = (typed: string): string | undefined => {
  const said = typed.trim();
  if (said === "") {
    return undefined;
  }
  if (/^[a-z][a-z0-9+.-]*:\/\//iu.test(said)) {
    return said;
  }
  // A bare host, with or without a port and a path. Local names get http,
  // because a dev server is the overwhelming case and none of them have a
  // certificate; everything else gets https.
  if (/^(localhost|127\.0\.0\.1|\[::1\])(:\d+)?(\/|$)/iu.test(said)) {
    return `http://${said}`;
  }
  if (/^[\w-]+(\.[\w-]+)+(:\d+)?(\/|\?|#|$)/u.test(said)) {
    return `https://${said}`;
  }
  return `https://duckduckgo.com/?q=${encodeURIComponent(said)}`;
};
