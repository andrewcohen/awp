// amoeba: the reference awp. An Electrobun main process (Bun) over a
// Vite-built React renderer.
//
// The renderer talks to the daemon directly over a localhost socket using the
// protocol contract. Electrobun's own webview-to-main RPC is reserved for what
// only the native process can do — window and menu, file dialogs, keychain,
// deep links — so a call has one hop and one schema rather than two.

import { paneVersion } from "@awp-kit/pane";
import { protocolVersion } from "@awp-kit/protocol";
import { serverVersion } from "@awp-kit/server";

export const versions = { protocolVersion, serverVersion, paneVersion };
