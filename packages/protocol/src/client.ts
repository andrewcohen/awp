// The other end of the contract.
//
// Lives beside the contract rather than in the renderer because there is
// nothing renderer-shaped about it: the same client serves a browser, a test,
// and a command-line tool. Everything it knows about the daemon's procedures is
// derived from `AwpRpcs`, so a change to a payload breaks here at compile time
// rather than at the first call.

import { Context, Effect, Layer } from "effect";
import { Socket } from "effect/unstable/socket";
import { RpcClient, RpcSerialization } from "effect/unstable/rpc";
import { AwpRpcs } from "./index";

export const DEFAULT_DAEMON_URL = "ws://127.0.0.1:5274";

/**
 * A client for the daemon at `url`.
 *
 * The serialization has to be the one the daemon uses. It is stated in both
 * places rather than shared through a constant, deliberately: they are two
 * processes that could be different builds, and a shared constant would hide a
 * mismatch behind an import instead of surfacing it as garbled frames.
 * Changing one without the other is a thing that must be *noticed*, and the
 * only honest way to notice it is a version handshake — which is what
 * `protocolVersion` is reserved for and does not do yet.
 *
 * ── it reconnects, and none of that is this file's doing ──────────────────
 *
 * The socket loop is already wrapped in `Effect.retryOrElse` inside
 * `makeProtocolSocket`, unconditionally — exponential from 500ms, capped at one
 * attempt every 5s — so a dropped connection reopens whether or not anything
 * here asks for it. That was true before the window could survive a daemon
 * restart, which is worth stating plainly: **the socket was never the problem.**
 * What died was everything built on it, and `subscribe` and `onReconnect` in
 * the renderer's daemon.ts are where that is answered.
 *
 * `retryTransientErrors` was tried here and taken out again, because it does
 * the opposite of what its name suggests to a reader:
 *
 *   off (the default)   a SocketOpenError is broadcast, `currentError` is set,
 *                       and every later `send` fails at once — so a pending
 *                       `listSessions()` rejects and the window can say so
 *   on                  the error goes to a hook instead, `currentError` stays
 *                       unset, and `send` writes into a dead socket — a call
 *                       made during the outage simply never settles
 *
 * A window has to be able to tell somebody the daemon is gone, and it cannot do
 * that from a promise that hangs. `onConnect` clears `currentError`, so failing
 * fast costs nothing on the way back.
 *
 * Measured rather than reasoned about: with the flag removed, a window whose
 * daemon was killed and restarted came back in three seconds, sidebar and all.
 */
export const layer = (url: string = DEFAULT_DAEMON_URL) =>
  RpcClient.layerProtocolSocket().pipe(
    Layer.provide(RpcSerialization.layerNdjson),
    Layer.provide(Socket.layerWebSocket(url)),
    // `globalThis.WebSocket` — present in a webview and in Bun alike, which is
    // why this same layer works for the renderer and for a script.
    Layer.provide(Socket.layerWebSocketConstructorGlobal),
  );

/** The generated client, with one method per procedure in {@link AwpRpcs}. */
export const make = RpcClient.make(AwpRpcs);

export type AwpClientShape = Effect.Success<typeof make>;

/**
 * The client as a service, so an application holds one rather than one per
 * call.
 *
 * It matters more than tidiness here: building a client opens the socket, and a
 * client per call would mean a connection per keystroke. Behind a tag it is
 * also swappable for a fake, which is the only way a component that talks to
 * the daemon can be tested without one.
 */
export class AwpClient extends Context.Service<AwpClient, AwpClientShape>()("awp/AwpClient") {}

/**
 * Say when the socket opens and when it goes.
 *
 * The real signal, rather than the proxy the window had been using — whether
 * the last session listing happened to fail. That answered "did a request work"
 * and was read as "is the daemon there", which are the same only until a call
 * succeeds against a connection that dies a moment later.
 *
 * It matters more than a status word now that the client reconnects: something
 * has to re-ask for the things that are answers rather than feeds — the session
 * list, the threads — and `onConnect` is the only honest moment to do it.
 *
 * `onConnect` fires on the *first* connection as well as every later one, so a
 * listener must be idempotent and must not assume it is a reconnection.
 */
export const layerConnection = (on: {
  readonly opened: () => void;
  readonly lost: () => void;
}): Layer.Layer<RpcClient.ConnectionHooks> =>
  Layer.succeed(RpcClient.ConnectionHooks)({
    onConnect: Effect.sync(on.opened),
    onDisconnect: Effect.sync(on.lost),
  });

/**
 * Everything a caller needs: the socket, the serialization and the client.
 *
 * `hooks` is optional because a script or a test wants a client and does not
 * care when it connected; the window does, and passes {@link layerConnection}.
 */
export const layerClient = (
  url: string = DEFAULT_DAEMON_URL,
  hooks?: Layer.Layer<RpcClient.ConnectionHooks>,
) =>
  Layer.effect(AwpClient)(make).pipe(
    Layer.provide(hooks === undefined ? layer(url) : layer(url).pipe(Layer.provide(hooks))),
  );
