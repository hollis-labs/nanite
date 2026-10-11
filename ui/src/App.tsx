import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AppGate } from "./components/AppGate";
import { AuthGate } from "./components/AuthGate";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { ThemeEffect } from "./hooks/useTheme";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 1000 * 60,
      retry: 1,
    },
  },
});

function App() {
  return (
    <ErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <ThemeEffect />
        <AuthGate>
          <AppGate />
        </AuthGate>
      </QueryClientProvider>
    </ErrorBoundary>
  );
}

export default App;
