import ReactDOM from "react-dom/client";
import "./index.css";
import App from "./App";

// StrictMode is off. In gdeck it had to be, because double-mounting killed the
// wasm terminal; tdeck has no terminal, so the original reason is gone.
//
// It stays off for a smaller one: an EventSource opened in an effect is
// connected, torn down and reconnected on every mount, and the backend replays
// a conversation to each new subscriber. That is correct behaviour on both
// sides and still means every render of a chat costs two replays. Worth
// revisiting once the streams are subscription-counted rather than per-mount.
ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(<App />);
