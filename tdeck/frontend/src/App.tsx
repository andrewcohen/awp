import { useEffect, useState } from "react";
import { Boundary } from "./Boundary";
import { api, type SessionSummary } from "./api";

// Placeholder. Proves the scaffold — Tailwind, shadcn tokens, the dev proxy to
// the Bun server — and is replaced by the real three-panel layout.
export default function App() {
  const [sessions, setSessions] = useState<SessionSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .sessions()
      .then(setSessions)
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)));
  }, []);

  return (
    <Boundary>
      <div className="min-h-screen bg-background p-8 text-foreground">
        <h1 className="mb-4 text-lg font-semibold">tdeck</h1>
        {error && <p className="text-destructive">{error}</p>}
        {!sessions && !error && <p className="text-muted-foreground">loading…</p>}
        <ul className="space-y-1">
          {sessions?.map((session) => (
            <li key={session.sessionId} className="text-sm">
              <span className="text-muted-foreground">{session.sessionId.slice(0, 8)}</span>{" "}
              {session.title}
            </li>
          ))}
        </ul>
      </div>
    </Boundary>
  );
}
