import { contextBridge, ipcRenderer } from "electron";
import { CH } from "../channels";

// One function, in a stranger's page.
//
// This preload runs in every page the web panel visits, which is somebody
// else's site with somebody else's scripts in it. So it is sandboxed, context
// isolated, and exposes exactly one thing: a way to say something back to the
// window.
//
// It is the counterpart of Electrobun's `__electrobunSendToHost`, which every
// BrowserView got from its preload — the annotator was built on that and the
// name is kept as an alias, because the injected picker is a *string* that has
// to work against either host and the cost of the alias is one line.
//
// **What comes back is not trusted, and cannot be made so from here.** Any
// script on the page can call this with anything. `messageFrom` in the renderer
// is what decides whether a message is ours, by a marker, before a single field
// of it is read — see the note there about a stranger's object reaching a
// prompt typed at an agent.

const send = (message: unknown): void => {
  ipcRenderer.send(CH.fromGuest, message);
};

contextBridge.exposeInMainWorld("__awpSendToHost", send);
contextBridge.exposeInMainWorld("__electrobunSendToHost", send);
