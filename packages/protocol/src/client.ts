// The other end of the contract.
//
// Lives beside the contract rather than in the renderer because there is
// nothing renderer-shaped about it: the same client serves a browser, a test,
// and a command-line tool. Everything it knows about the daemon's procedures is
// derived from `AwpRpcs`, so a change to a payload breaks here at compile time
// rather than at the first call.

import { Context, type Effect, Layer } from "effect";
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

/** Everything a caller needs: the socket, the serialization and the client. */
export const layerClient = (url: string = DEFAULT_DAEMON_URL) =>
  Layer.effect(AwpClient)(make).pipe(Layer.provide(layer(url)));
