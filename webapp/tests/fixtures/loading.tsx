import "../../src/styles/reset.css";
import "../../src/styles/tokens.css";
import "../../src/styles/base.css";
import "./loading.css";

import { useState } from "react";
import { createRoot } from "react-dom/client";

import { Button, type ButtonVariant } from "../../src/components/Button/Button";
import { Loading } from "../../src/components/Loading/Loading";

function Action({ variant }: { variant: ButtonVariant }) {
  const [pending, setPending] = useState(false);
  const [attempts, setAttempts] = useState(0);
  return <section data-testid={`action-${variant}`}>
    <Button variant={variant} isLoading={pending} data-testid={`button-${variant}`}
      loadingLabel="Saving your changes and waiting for confirmation…"
      onClick={() => { setPending(true); setAttempts((value) => value + 1); }}>
      Save these account preferences
    </Button>
    <button data-testid={`finish-${variant}`} onClick={() => setPending(false)}>Finish</button>
    <output>{attempts}</output>
  </section>;
}

function Fixture() {
  return <main>
    <h1>Loading component fixture</h1>
    <p data-testid="inline-parent">Before <Loading label="Loading inline…" size="small" /> after</p>
    <div className="panels">
      <div data-testid="compact-parent" className="compact"><Loading label="Loading compact content…" /></div>
      <div data-testid="message-parent" className="message">
        <Loading label="Checking the available options for your account, including all the methods enabled by your institution…" showLabel size="large" />
      </div>
    </div>
    {(["primary", "secondary", "text"] as const).map((variant) => <Action key={variant} variant={variant} />)}
  </main>;
}

const root = document.getElementById("root");
if (root === null) throw new Error("Loading fixture root is missing");
createRoot(root).render(<Fixture />);
