import { expect, request } from "@playwright/test";

import { mailpitURL, serverURL } from "./environment";

interface MessageSummary { ID: string }
interface Message { To: Array<{ Address: string }>; Subject: string; Text: string }

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// mailedLink waits until Mailpit holds a message to `recipient` whose text
// contains a canonical-origin link to `pathname`, and returns that link.
// Each test uses unique recipients, so the search is unambiguous.
export async function mailedLink(recipient: string, pathname: string): Promise<string> {
  const mailpit = await request.newContext({ baseURL: mailpitURL });
  const pattern = new RegExp(`${escapeRegExp(serverURL + pathname)}#token=[A-Za-z0-9_%-]{43,}`);
  let link = "";
  try {
    await expect.poll(async () => {
      const search = await mailpit.get("/api/v1/search", {
        params: { query: `to:"${recipient}"`, limit: "20" },
      });
      if (!search.ok()) return "";
      const { messages } = (await search.json()) as { messages: MessageSummary[] };
      for (const summary of messages) {
        const response = await mailpit.get(`/api/v1/message/${encodeURIComponent(summary.ID)}`);
        if (!response.ok()) continue;
        const message = (await response.json()) as Message;
        if (!message.To.some(({ Address }) => Address.toLowerCase() === recipient.toLowerCase())) continue;
        const match = message.Text.match(pattern);
        if (match) {
          link = match[0];
          return link;
        }
      }
      return "";
    }, { message: `mail to ${recipient} with a ${pathname} link`, timeout: 30_000 }).not.toBe("");
  } finally {
    await mailpit.dispose();
  }
  return link;
}

// claimFrom extracts the one-time token carried in a mailed link fragment.
export function claimFrom(link: string): string {
  return decodeURIComponent(new URL(link).hash.replace(/^#token=/, ""));
}
