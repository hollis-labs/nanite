import { type FormEvent, type ReactNode, useCallback, useEffect, useState } from "react";
import { fetchSession, installAuthFetch, login, UNAUTHENTICATED_EVENT } from "@/lib/auth";

type State = "checking" | "signed-in" | "signed-out";

/**
 * Gates the app behind a signed-in session. Nanite's API is authenticated on
 * every route, loopback included; the browser signs in once with the operator
 * credential (or a one-time link from `nanite auth login-link`) and then
 * holds only an HttpOnly cookie.
 */
export function AuthGate({ children }: { children: ReactNode }) {
  const [state, setState] = useState<State>("checking");

  const check = useCallback(async () => {
    try {
      setState((await fetchSession()) ? "signed-in" : "signed-out");
    } catch {
      setState("signed-out");
    }
  }, []);

  useEffect(() => {
    installAuthFetch();
    void check();
    const onUnauthenticated = () => setState("signed-out");
    window.addEventListener(UNAUTHENTICATED_EVENT, onUnauthenticated);
    return () => window.removeEventListener(UNAUTHENTICATED_EVENT, onUnauthenticated);
  }, [check]);

  if (state === "checking") return <div className="h-screen w-screen bg-bg" />;
  if (state === "signed-out") return <LoginScreen onSignedIn={() => setState("signed-in")} />;
  return <>{children}</>;
}

function LoginScreen({ onSignedIn }: { onSignedIn: () => void }) {
  const [token, setToken] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!token.trim()) return;
    setBusy(true);
    setError(null);
    try {
      await login(token);
      setToken("");
      onSignedIn();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Sign-in failed.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="flex h-screen w-screen flex-col items-center justify-center bg-bg px-8">
      <form onSubmit={(event) => void submit(event)} className="w-full max-w-sm space-y-3">
        <h1 className="text-2xl font-semibold text-fg">Sign in to Nanite</h1>
        <p className="text-sm text-fg-muted">
          Paste your operator credential, or run <code>nanite auth login-link</code> for a one-time
          link. The operator token file is at the path printed by{" "}
          <code>nanite auth token-path</code>.
        </p>
        <input
          type="password"
          autoComplete="off"
          value={token}
          onChange={(event) => setToken(event.target.value)}
          placeholder="nan_operator_…"
          aria-label="Operator credential"
          className="w-full rounded-md border border-border-subtle bg-surface px-3 py-2 font-mono text-sm text-fg focus:outline-none focus:ring-1 focus:ring-primary"
        />
        {error && (
          <p role="alert" className="text-xs text-danger">
            {error}
          </p>
        )}
        <button
          type="submit"
          disabled={busy || !token.trim()}
          className="w-full rounded-md bg-primary px-3 py-2 text-sm text-white hover:bg-primary-hover disabled:opacity-60"
        >
          {busy ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </main>
  );
}
