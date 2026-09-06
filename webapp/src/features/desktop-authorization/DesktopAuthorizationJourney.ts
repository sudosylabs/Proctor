import { useEffect, useRef, useSyncExternalStore } from "react";

import { createAsyncResourceController } from "../../app/AsyncResource";
import {
  approveDesktopAuthorization,
  authenticateDesktopAuthorizationLocally,
  authenticateDesktopAuthorizationSession,
  bindDesktopAuthorization,
  cancelDesktopAuthorization,
  desktopAuthorizationProviderURL,
  requestDesktopAuthorizationContext,
  resetDesktopAuthorizationAccount,
  type DesktopAuthorizationContext,
  type DesktopAuthorizationProof,
  type DesktopContextResult,
  type DesktopLocalAuthenticationSubmission,
} from "./DesktopAuthorizationApi";

export type DesktopTerminalState = "approved" | "cancelled" | "invalid" | "locked";
type PendingAction = "approve" | "cancel" | "reset" | "authenticate" | "provider";
export type DesktopLocalFeedback =
  | "authenticated"
  | "mfa_required"
  | "mfa_invalid"
  | "invalid_credentials"
  | "rate_limited"
  | "ignored";

export interface DesktopJourneySnapshot {
  view:
    | { kind: "checking" | "unavailable" | DesktopTerminalState }
    | { kind: "authentication" | "confirmation"; context: DesktopAuthorizationContext };
  pending?: PendingAction;
  feedback?: "unavailable";
  focusHeading: boolean;
}

interface DesktopJourneyActions {
  approve(): Promise<void>;
  cancel(): Promise<void>;
  useAnotherAccount(): Promise<void>;
  authenticate(
    submission: DesktopLocalAuthenticationSubmission,
  ): Promise<DesktopLocalFeedback>;
  chooseProvider(providerID: string): void;
  retry(): void;
}

export interface DesktopAuthorizationJourney extends DesktopJourneyActions {
  snapshot: DesktopJourneySnapshot;
}

interface JourneyController extends DesktopJourneyActions {
  getSnapshot(): DesktopJourneySnapshot;
  subscribe(listener: () => void): () => void;
  start(): Promise<DesktopContextResult>;
  stop(): void;
}

interface JourneyInput {
  proof?: DesktopAuthorizationProof;
  state?: string;
  servingOrigin: string;
}

export const desktopJourneyAPI = {
  approve: approveDesktopAuthorization,
  authenticate: authenticateDesktopAuthorizationLocally,
  authenticateSession: authenticateDesktopAuthorizationSession,
  bind: bindDesktopAuthorization,
  cancel: cancelDesktopAuthorization,
  context: requestDesktopAuthorizationContext,
  providerURL: desktopAuthorizationProviderURL,
  reset: resetDesktopAuthorizationAccount,
};

// The feature owns transaction sequencing. Presentation owns form values and
// field validation; the shared resource owns initial-load/retry generations.
export function createDesktopAuthorizationJourney(
  input: JourneyInput,
  api = desktopJourneyAPI,
  navigate: (url: string, replace: boolean) => void = (url, replace) => {
    if (replace) window.location.replace(url);
    else window.location.assign(url);
  },
): JourneyController {
  let bindingEstablished = input.proof === undefined;
  let useWebSession = true;
  let active = false;
  let generation = 0;
  let pending: PendingAction | undefined;
  let terminal: DesktopTerminalState | undefined;
  let feedback: "unavailable" | undefined;
  let focusHeading = false;
  const listeners = new Set<() => void>();

  async function load(): Promise<DesktopContextResult> {
    try {
      if (input.state === undefined) return { kind: "invalid" };
      if (!bindingEstablished && input.proof !== undefined) {
        const bound = await api.bind(input.proof);
        if (bound.kind !== "bound") return bound;
        bindingEstablished = true;
      }
      const loaded = await api.context(input.servingOrigin);
      if (loaded.kind !== "ready" || loaded.context.state !== "bound" || !useWebSession) {
        return loaded;
      }
      const session = await api.authenticateSession(loaded.context.installation);
      if (session.kind === "authenticated") {
        return { kind: "ready", context: session.context };
      }
      if (session.kind === "no_session") return loaded;
      if (session.kind === "invalid" || session.kind === "locked") return session;
      return { kind: "unavailable" };
    } catch {
      return { kind: "unavailable" };
    }
  }

  const resource = createAsyncResourceController<DesktopContextResult>(
    load,
    input.state === undefined ? { kind: "invalid" } : { kind: "unavailable" },
    input.state !== undefined,
  );
  let snapshot: DesktopJourneySnapshot;

  function currentView(): DesktopJourneySnapshot["view"] {
    if (terminal !== undefined) return { kind: terminal };
    const current = resource.getSnapshot();
    if (current.loading) return { kind: "checking" };
    if (current.value.kind !== "ready") return { kind: current.value.kind };
    return {
      kind: current.value.context.state === "bound" ? "authentication" : "confirmation",
      context: current.value.context,
    };
  }

  function publish() {
    snapshot = { view: currentView(), pending, feedback, focusHeading };
    for (const listener of listeners) listener();
  }
  resource.subscribe(publish);

  function failed(result: { kind: string }) {
    if (result.kind === "invalid" || result.kind === "locked") {
      terminal = result.kind;
      focusHeading = true;
    } else {
      feedback = "unavailable";
    }
  }

  async function perform<T>(
    action: PendingAction,
    work: () => Promise<T>,
    apply: (result: T) => void | Promise<void>,
  ) {
    if (!active || pending !== undefined || terminal !== undefined || resource.getSnapshot().loading) {
      return;
    }
    const attempt = ++generation;
    pending = action;
    feedback = undefined;
    publish();
    try {
      const result = await work();
      if (active && generation === attempt) await apply(result);
    } catch {
      if (active && generation === attempt) failed({ kind: "unavailable" });
    } finally {
      if (active && generation === attempt) {
        pending = undefined;
        publish();
      }
    }
  }

  function currentContext(state: DesktopAuthorizationContext["state"]) {
    const current = resource.getSnapshot();
    return !current.loading && current.value.kind === "ready" && current.value.context.state === state
      ? current.value.context : undefined;
  }

  return {
    getSnapshot: () => snapshot,
    subscribe(listener) {
      listeners.add(listener);
      return () => { listeners.delete(listener); };
    },
    start() {
      active = true;
      publish();
      if (input.state === undefined) return Promise.resolve({ kind: "invalid" });
      return resource.start();
    },
    stop() {
      active = false;
      generation += 1;
      pending = undefined;
    },
    retry() {
      if (!active || pending !== undefined || terminal !== undefined || resource.getSnapshot().loading ||
        resource.getSnapshot().value.kind !== "unavailable") return;
      focusHeading = true;
      feedback = undefined;
      void resource.retry();
    },
    async approve() {
      if (input.state === undefined || currentContext("authenticated") === undefined) return;
      await perform("approve", () => api.approve(input.state!), (result) => {
        if (result.kind === "approved") {
          terminal = "approved";
          focusHeading = true;
          publish();
          navigate(result.redirectURL, true);
        } else failed(result);
      });
    },
    async cancel() {
      if (input.state === undefined || resource.getSnapshot().value.kind !== "ready") return;
      await perform("cancel", () => api.cancel(input.state!), (result) => {
        if (result.kind === "cancelled") {
          terminal = "cancelled";
          focusHeading = true;
        } else failed(result);
      });
    },
    async useAnotherAccount() {
      if (currentContext("authenticated") === undefined) return;
      await perform("reset", () => {
        // A reset can commit even when its response is lost. Never retain the
        // old account confirmation or automatically reapply the Web Session.
        useWebSession = false;
        focusHeading = true;
        resource.replace({ kind: "unavailable" });
        return api.reset();
      }, async (result) => {
        if (result.kind === "reset") await resource.retry();
        else failed(result);
      });
    },
    async authenticate(submission) {
      const context = currentContext("bound");
      if (context === undefined || !context.localLoginEnabled) return "ignored";
      let local: DesktopLocalFeedback = "ignored";
      await perform("authenticate", () => api.authenticate(context.installation, submission), (result) => {
        switch (result.kind) {
          case "authenticated":
            local = "authenticated";
            focusHeading = true;
            resource.replace({ kind: "ready", context: result.context });
            break;
          case "mfa_required":
          case "mfa_invalid":
          case "invalid_credentials":
          case "rate_limited":
            local = result.kind;
            break;
          default:
            failed(result);
        }
      });
      return local;
    },
    chooseProvider(providerID) {
      const context = currentContext("bound");
      if (!active || pending !== undefined || terminal !== undefined || input.state === undefined ||
        context === undefined || !context.externalProviders.some((provider) => provider.id === providerID)) return;
      pending = "provider";
      feedback = undefined;
      publish();
      try {
        navigate(api.providerURL(providerID, input.state), false);
      } catch {
        pending = undefined;
        feedback = "unavailable";
        publish();
      }
    },
  };
}

export function useDesktopAuthorizationJourney(input: JourneyInput): DesktopAuthorizationJourney {
  const ref = useRef<JourneyController>(undefined);
  ref.current ??= createDesktopAuthorizationJourney(input);
  const controller = ref.current;
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  useEffect(() => {
    void controller.start();
    return controller.stop;
  }, [controller]);
  return { ...controller, snapshot };
}
