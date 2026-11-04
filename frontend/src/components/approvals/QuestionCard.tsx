"use client";
import type { QuestionItem } from "@/lib/questionsLogic";
import { QuestionPending, WaitingLabel, type PendingVariant } from "./PendingCard";

export { WaitingLabel };

/** A question an agent asked ("Necesita tu ayuda"), drawn by the one pending card (`PendingCard`). Kept as its own name
 * for the screens that import it (project follow-up, node drawer, Pendientes). */
export function QuestionCard(props: { q: QuestionItem; variant?: PendingVariant; selectable?: boolean; selected?: boolean; onSelect?: (v: boolean) => void }) {
  return <QuestionPending {...props} />;
}
