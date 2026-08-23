import type { HostedPageBootstrap } from "./bootstrap";

export interface AppProps {
  bootstrap: HostedPageBootstrap;
}

// The visual hosted-page implementation is deliberately deferred. The root
// still receives sanitized, purpose-specific bootstrap state so future pages
// never need to recover credentials from browser history.
export function App({ bootstrap }: AppProps) {
  void bootstrap;
  return null;
}
