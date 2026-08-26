import { Component, type ReactNode } from "react";

interface FatalErrorBoundaryProps {
  children: ReactNode;
  initialFailure?: boolean;
}

interface FatalErrorBoundaryState {
  failed: boolean;
}

export class FatalErrorBoundary extends Component<
  FatalErrorBoundaryProps,
  FatalErrorBoundaryState
> {
  state: FatalErrorBoundaryState = {
    failed: this.props.initialFailure ?? false,
  };

  static getDerivedStateFromError(): FatalErrorBoundaryState {
    return { failed: true };
  }

  componentDidCatch() {
    // The exception may contain sensitive page state. The boundary deliberately
    // renders bounded recovery copy without forwarding the exception to logs.
  }

  render() {
    if (this.state.failed) {
      return (
        <main id="main-content" tabIndex={-1} aria-labelledby="fatal-title">
          <h1 id="fatal-title">Proctor could not load</h1>
          <p>Reload this page to try again.</p>
          <button type="button" onClick={() => window.location.reload()}>
            Reload page
          </button>
        </main>
      );
    }

    return this.props.children;
  }
}
