import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { FatalErrorBoundary } from "../../src/app/FatalErrorBoundary";
import { redactedRootErrorOptions } from "../../src/app/rootErrors";
import "../../src/styles/reset.css";
import "../../src/styles/tokens.css";
import "../../src/styles/base.css";

function BrokenFixture(): never {
  throw new Error("Fixture render failed");
}

const root = document.getElementById("root");
if (root === null) {
  throw new Error("Fatal boundary fixture root is missing");
}

createRoot(root, redactedRootErrorOptions).render(
  <StrictMode>
    <FatalErrorBoundary>
      <BrokenFixture />
    </FatalErrorBoundary>
  </StrictMode>,
);
