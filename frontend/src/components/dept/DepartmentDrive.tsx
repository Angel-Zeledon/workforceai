"use client";
import { FilesDepartmentHome, FilesScreen } from "../files/FilesScreen";

/**
 * `/d` and `/d/<office>`: the direct links of a department. They open the one Files screen (components/files/FilesScreen)
 * at the department's folder, with its own tools (Para revisar, the composer, what other departments share with it).
 */
export function DepartmentHome() {
  return <FilesDepartmentHome />;
}

export function DepartmentDrive({ officeId }: { officeId: string }) {
  return <FilesScreen officeId={officeId} />;
}
