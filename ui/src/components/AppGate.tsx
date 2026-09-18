import { useState } from "react";
import { SetupWizard } from "@/components/chat/SetupWizard";
import { useSettings } from "@/hooks/useSettings";
import { AppShell } from "./AppShell";

const SETUP_WIZARD_DISMISSED_KEY = "nanite:setupWizardDismissed";

function readDismissed(): boolean {
  try {
    return localStorage.getItem(SETUP_WIZARD_DISMISSED_KEY) === "1";
  } catch {
    return false;
  }
}

/**
 * Gates the whole app shell — sidebars, nav rail, chat, every AppShell side
 * effect (SSE, plugin registry, tool refresh) — behind knowing whether
 * first-run setup is needed. Settings still loading (settings===undefined)
 * must NOT read as "no setup needed": that gap is what let the full app
 * flash on screen before the wizard swapped in when this lived inside
 * WelcomeScreen instead of here, above AppShell entirely.
 */
export function AppGate() {
  const { data: settings, isLoading } = useSettings();
  const [dismissed, setDismissed] = useState(readDismissed);

  if (isLoading) {
    return <div className="h-screen w-screen bg-bg" />;
  }

  const needsSetup = settings?.default_provider === "" && !dismissed;
  if (needsSetup) {
    return (
      <main className="flex h-screen w-screen flex-col items-center justify-center bg-bg px-8">
        <div className="mb-10 w-full max-w-lg text-center">
          <h1 className="mb-2 text-2xl font-semibold text-fg">Welcome to Nanite</h1>
          <p className="text-sm text-fg-muted">Let's get you connected to an agent.</p>
        </div>
        <SetupWizard
          onDismiss={() => {
            try {
              localStorage.setItem(SETUP_WIZARD_DISMISSED_KEY, "1");
            } catch {
              // best-effort — private windows / blocked storage just re-show the wizard next time
            }
            setDismissed(true);
          }}
        />
      </main>
    );
  }

  return <AppShell />;
}
