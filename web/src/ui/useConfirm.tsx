import { useState, type ReactNode } from "react";

import { ConfirmDialog } from "./ConfirmDialog";

export interface Question {
  readonly title: string;
  /** What is lost or changed, in words. */
  readonly consequence: ReactNode;
  readonly confirmLabel: string;
  readonly danger?: boolean;
  readonly run: () => void;
}

/** The one way an irreversible action asks first: call `ask` from the handler and render `dialog` once in the page. */
export function useConfirm(): { ask: (question: Question) => void; dialog: ReactNode } {
  const [question, setQuestion] = useState<Question | null>(null);
  const dialog = question && (
    <ConfirmDialog
      title={question.title}
      consequence={question.consequence}
      confirmLabel={question.confirmLabel}
      danger={question.danger ?? true}
      onCancel={() => setQuestion(null)}
      onConfirm={() => {
        setQuestion(null);
        question.run();
      }}
    />
  );
  return { ask: setQuestion, dialog };
}
