import { AccessPageShell } from "../../components/AccessPageShell/AccessPageShell";
import { EmailChangeUndoContent } from "../../components/EmailChangeUndo/EmailChangeUndoContent";
import { message } from "../../i18n/messages";
import { undoEmailChange } from "./UndoEmailChangeApi";

export interface UndoEmailChangePageProps {
  token?: string;
}

export function UndoEmailChangePage({ token }: UndoEmailChangePageProps) {
  return (
    <AccessPageShell
      skipLabel={message("webapp.undo_email_change.skip_to_main")}
      variant="status"
    >
      <EmailChangeUndoContent token={token} undoEmailChange={undoEmailChange} />
    </AccessPageShell>
  );
}
