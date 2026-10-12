"use client";
import { useT } from "@/lib/i18n";
import { useSkillsNav } from "@/lib/skills";
import { fromTask } from "@/lib/skillsLogic";
import { useCan } from "@/lib/session";
import type { Task } from "@/lib/types";
import { Btn } from "../ui";

/** "Save as skill" on a finished task: opens the editor pre-filled from the task and its output
 * (the deliverable becomes an example). Nothing is saved until a person presses Create. */
export function SaveAsSkillButton({ task }: { task: Task }) {
  const { t } = useT();
  const can = useCan("skills:write");
  if (!can || task.status !== "done" || !task.output) return null;
  const labels = {
    whenToUse: t("skills.fromWork.whenToUse"), context: t("skills.fromWork.context"), approach: t("skills.fromWork.approach"), steps: t("skills.fromWork.steps"),
    quality: t("skills.fromWork.quality"), descriptionPrefix: t("skills.fromWork.descriptionPrefix"), example: t("skills.fromWork.changelog"),
  };
  return (
    <Btn data-testid={`save-as-skill-${task.id}`} className="mt-2" onClick={() => useSkillsNav.getState().openNew(fromTask(task, labels))}>{t("skills.fromWork.button")}</Btn>
  );
}
