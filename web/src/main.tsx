import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router';
import { MutationCache, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AuthProvider } from './auth.js';
import { ApiError } from './api.js';
import { pushToast } from './toast.js';
import { App } from './App.js';
import { ErrorBoundary, installClientErrorReporting } from './errors.js';
import { initTheme } from './theme.js';
import './index.css';

const queryClient = new QueryClient({
  // staleTime keeps back-navigation instant: cached screens render immediately and refresh in the
  // background (SSE invalidation still forces freshness where it matters).
  defaultOptions: { queries: { retry: false, staleTime: 30_000 } },
  // Surface every failed mutation as a toast (friendly message from the stable ApiError code).
  mutationCache: new MutationCache({
    onError: (err) => pushToast(err instanceof ApiError ? `${err.code}: ${err.message}` : err instanceof Error ? err.message : String(err)),
  }),
});

// Event-handler throws and unhandled rejections never reach a React boundary — net them here so a
// silent `Uncaught (in promise)` becomes a tracked error group instead of a console line nobody reads.
installClientErrorReporting();

// BEFORE createRoot, not inside a component. A theme applied in an effect paints the wrong palette
// first and then corrects it — the flash every dark-mode user notices immediately.
initTheme();

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {/* OUTERMOST. Everything below can throw — the providers included — and this is the only thing
        between such a throw and an empty <div id="root">. */}
    <ErrorBoundary variant="full">
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <AuthProvider>
          <App />
        </AuthProvider>
      </BrowserRouter>
    </QueryClientProvider>
    </ErrorBoundary>
  </StrictMode>,
);
