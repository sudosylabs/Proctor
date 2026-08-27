import { useEffect, useState } from "react";

import type {
  BeginProviderConnectionResult,
  Provider,
  ProviderConnectionContextResult,
} from "../../features/connect-provider/ProviderConnectionApi";
import { message } from "../../i18n/messages";
import { AccessTaskIntro } from "../AccessTaskIntro/AccessTaskIntro";
import { Button, ButtonLink } from "../Button/Button";
import { FormFeedback } from "../FormFeedback/FormFeedback";
import { Notice } from "../Notice/Notice";
import {
  TaskState,
  TaskStateActions,
  TaskStateAnnouncement,
} from "../TaskState/TaskState";
import styles from "./ProviderConnection.module.css";

export interface ProviderConnectionContentProps {
  beginConnection(providerID: string): Promise<BeginProviderConnectionResult>;
  loading: boolean;
  onRedirect(url: string): void;
  onRetry(): void;
  state: ProviderConnectionContextResult;
}

export function ProviderConnectionContent({
  beginConnection,
  loading,
  onRedirect,
  onRetry,
  state,
}: ProviderConnectionContentProps) {
  const [retryRequested, setRetryRequested] = useState(false);

  function retry() {
    setRetryRequested(true);
    onRetry();
  }

  if (loading) {
    return (
      <>
        <TaskStateAnnouncement
          message={message("webapp.connect_provider.loading.heading")}
        />
        <ProviderRouteState
          busy
          heading={message("webapp.connect_provider.loading.heading")}
          body={message("webapp.connect_provider.loading.body")}
        />
      </>
    );
  }
  if (state.kind === "no_session") {
    return (
      <>
        <TaskStateAnnouncement
          message={message("webapp.connect_provider.no_session.heading")}
        />
        <ProviderRouteState
          focusHeading={retryRequested}
          heading={message("webapp.connect_provider.no_session.heading")}
          body={message("webapp.connect_provider.no_session.body")}
        >
          <TaskStateActions>
            <ButtonLink href="/login">
              {message("webapp.connect_provider.no_session.sign_in")}
            </ButtonLink>
          </TaskStateActions>
        </ProviderRouteState>
      </>
    );
  }
  if (state.kind === "unavailable") {
    return (
      <>
        <TaskStateAnnouncement
          message={message("webapp.connect_provider.unavailable.heading")}
        />
        <ProviderRouteState
          focusHeading={retryRequested}
          heading={message("webapp.connect_provider.unavailable.heading")}
          body={message("webapp.connect_provider.unavailable.body")}
        >
          <TaskStateActions>
            <Button onClick={retry}>
              {message("webapp.connect_provider.unavailable.retry")}
            </Button>
          </TaskStateActions>
        </ProviderRouteState>
      </>
    );
  }
  if (state.context.providers.length === 0) {
    return (
      <>
        <TaskStateAnnouncement
          message={message("webapp.connect_provider.empty.heading")}
        />
        <ProviderRouteState
          focusHeading={retryRequested}
          heading={message("webapp.connect_provider.empty.heading")}
          body={message("webapp.connect_provider.empty.body")}
        >
          <TaskStateActions>
            <a className={styles.returnLink} href="/authorization/complete">
              {message("webapp.connect_provider.return")}
            </a>
          </TaskStateActions>
        </ProviderRouteState>
      </>
    );
  }
  return (
    <>
      <TaskStateAnnouncement message={message("webapp.connect_provider.heading")} />
      <ProviderChooser
        focusHeading={retryRequested}
        providers={state.context.providers}
        beginConnection={beginConnection}
        onRedirect={onRedirect}
      />
    </>
  );
}

function ProviderChooser({
  beginConnection,
  focusHeading,
  onRedirect,
  providers,
}: {
  beginConnection(providerID: string): Promise<BeginProviderConnectionResult>;
  focusHeading: boolean;
  onRedirect(url: string): void;
  providers: Provider[];
}) {
  const [selectedID, setSelectedID] = useState(providers[0]?.id ?? "");
  const [pending, setPending] = useState(false);
  const [feedback, setFeedback] = useState<string>();
  const selected = providers.find((provider) => provider.id === selectedID);

  useEffect(() => {
    if (!providers.some((provider) => provider.id === selectedID)) {
      setSelectedID(providers[0]?.id ?? "");
    }
  }, [providers, selectedID]);

  async function connect() {
    if (pending || selected === undefined) {
      return;
    }
    setPending(true);
    setFeedback(undefined);
    const result = await beginConnection(selected.id);
    if (result.kind === "redirect") {
      onRedirect(result.url);
      return;
    }
    setFeedback(
      message(
        result.kind === "reauthentication_required"
          ? "webapp.connect_provider.error.reauthentication"
          : "webapp.connect_provider.error.unavailable",
      ),
    );
    setPending(false);
  }

  return (
    <section className={styles.page} aria-labelledby="connect-provider-heading">
      <AccessTaskIntro
        eyebrow={message("webapp.connect_provider.context.eyebrow")}
        heading={message("webapp.connect_provider.context.heading")}
        body={message("webapp.connect_provider.context.body")}
        focusHeading={focusHeading}
        headingID="connect-provider-heading"
      />
      <header className={styles.headingGroup}>
        <h2>{message("webapp.connect_provider.heading")}</h2>
        <p>{message("webapp.connect_provider.lede")}</p>
      </header>
      <fieldset className={styles.providerList}>
        <legend className={styles.visuallyHidden}>
          {message("webapp.connect_provider.group")}
        </legend>
        {providers.map((provider) => (
          <label
            key={provider.id}
            className={provider.id === selectedID ? styles.selected : undefined}
          >
            <input
              type="radio"
              name="provider"
              value={provider.id}
              checked={provider.id === selectedID}
              onChange={() => {
                setSelectedID(provider.id);
                setFeedback(undefined);
              }}
            />
            <span className={styles.providerName}>{provider.display_name}</span>
            <span className={styles.providerType}>
              {providerType(provider.type)}
            </span>
          </label>
        ))}
      </fieldset>
      <Button
        className={styles.connectButton}
        isLoading={pending}
        loadingLabel={message("webapp.connect_provider.connecting")}
        onClick={connect}
      >
        {message("webapp.connect_provider.connect", {
          Provider: selected?.display_name ?? "",
        })}
      </Button>
      <div className={styles.noticeGroup}>
        <Notice role="note">
          {message("webapp.connect_provider.evidence")}
        </Notice>
        <Notice role="note">
          {message("webapp.connect_provider.context.note")}
        </Notice>
      </div>
      <a className={styles.returnLink} href="/authorization/complete">
        {message("webapp.connect_provider.return")}
      </a>
      <FormFeedback message={feedback} />
    </section>
  );
}

function providerType(type: string): string {
  switch (type.toLowerCase()) {
    case "oidc":
      return message("webapp.connect_provider.type.oidc");
    case "cas":
      return message("webapp.connect_provider.type.cas");
    default:
      return message("webapp.connect_provider.type.external");
  }
}

function ProviderRouteState({
  body,
  busy = false,
  children,
  focusHeading = false,
  heading,
}: {
  body: string;
  busy?: boolean;
  children?: React.ReactNode;
  focusHeading?: boolean;
  heading: string;
}) {
  return (
    <TaskState
      body={body}
      busy={busy}
      className={styles.taskState}
      focusHeading={focusHeading}
      heading={heading}
      headingID="connect-provider-heading"
    >
      {children}
    </TaskState>
  );
}
