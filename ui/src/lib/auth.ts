// Browser side of Nanite's API authentication (CW-20261011-0036).
//
// The server authenticates the UI with an HttpOnly session cookie; this module
// never sees the long-lived credential after login. Unsafe same-origin
// requests carry the CSRF token the server issued with the session, and a 401
// from the API tells the app to show the login screen.

let csrfToken = "";

export const UNAUTHENTICATED_EVENT = "nanite:unauthenticated";

export function setCsrfToken(token: string): void {
  csrfToken = token;
}

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

function requestUrl(input: RequestInfo | URL): string {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.href;
  return input.url;
}

function isSameOrigin(url: string): boolean {
  return url.startsWith("/") || url.startsWith(window.location.origin);
}

let installed = false;

/** Wrap window.fetch once: add the CSRF header and surface 401s. */
export function installAuthFetch(): void {
  if (installed) return;
  installed = true;
  const original = window.fetch.bind(window);
  window.fetch = async (input, init) => {
    const url = requestUrl(input);
    const sameOrigin = isSameOrigin(url);
    const method = (
      init?.method ?? (input instanceof Request ? input.method : "GET")
    ).toUpperCase();
    let nextInit = init;
    if (sameOrigin && csrfToken && !SAFE_METHODS.has(method)) {
      const headers = new Headers(
        init?.headers ?? (input instanceof Request ? input.headers : undefined),
      );
      headers.set("X-Nanite-CSRF", csrfToken);
      nextInit = { ...init, headers };
    }
    const response = await original(input, nextInit);
    if (
      response.status === 401 &&
      sameOrigin &&
      url.includes("/api/") &&
      !url.includes("/api/auth/")
    ) {
      window.dispatchEvent(new Event(UNAUTHENTICATED_EVENT));
    }
    return response;
  };
}

export interface AuthSession {
  csrf_token?: string;
  kind: string;
  label: string;
  scopes: string[];
}

/** The current session, or null when the browser is not signed in. */
export async function fetchSession(): Promise<AuthSession | null> {
  const response = await fetch("/api/auth/session");
  if (response.status === 401) return null;
  if (!response.ok) throw new Error(`session check failed (${response.status})`);
  const session = (await response.json()) as AuthSession;
  if (session.csrf_token) setCsrfToken(session.csrf_token);
  return session;
}

/** Exchange a pasted credential for a session cookie. */
export async function login(token: string): Promise<void> {
  const response = await fetch("/api/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token: token.trim() }),
  });
  if (!response.ok) throw new Error("That credential was not accepted.");
  const body = (await response.json()) as { csrf_token: string };
  setCsrfToken(body.csrf_token);
}

export async function logout(): Promise<void> {
  await fetch("/api/auth/logout", { method: "POST" });
  setCsrfToken("");
  window.dispatchEvent(new Event(UNAUTHENTICATED_EVENT));
}
