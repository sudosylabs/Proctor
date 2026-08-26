import type { RootOptions } from "react-dom/client";

// React's default root handlers forward exception objects to the browser
// console. Hosted-page errors may carry sensitive state, so the root renders
// bounded recovery without serializing those objects into ordinary logs.
export const redactedRootErrorOptions: RootOptions = {
  onCaughtError() {},
  onUncaughtError() {},
  onRecoverableError() {},
};
