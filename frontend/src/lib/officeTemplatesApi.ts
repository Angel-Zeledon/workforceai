import { call } from "./api";
import { MOCK } from "./config";
import type {
  OfficeTemplateCard, OfficeTemplateExport, OfficeTemplateFile, OfficeTemplateInstall, OfficeTemplatePreview,
} from "./officeTemplatesLogic";

/** Contract: backend/internal/api/office_templates.go (F3, /api/v1/office-templates). */
const mock = async () => (await import("./mock/office-templates-mock")).officeTemplatesMock;
const q = (officeId?: string) => (officeId ? `?office_id=${encodeURIComponent(officeId)}` : "");

export const officeTemplatesApi = {
  /** the file of a department (officeId; "general" = General) or of the whole company (officeId undefined + company) */
  exportFile: async (scope: { officeId?: string; company?: boolean }): Promise<OfficeTemplateExport> => {
    const path = scope.company ? "/office-templates/export?scope=company" : `/office-templates/export${q(scope.officeId ?? "general")}`;
    return MOCK ? (await mock()).exportFile(scope) : call<OfficeTemplateExport>("GET", path);
  },
  /** uploads a template file; the answer is its preview for this organization */
  importFile: async (file: OfficeTemplateFile): Promise<OfficeTemplatePreview> =>
    MOCK ? (await mock()).importFile(file) : call<OfficeTemplatePreview>("POST", "/office-templates/import", file),
  list: async (): Promise<OfficeTemplateCard[]> => (MOCK ? (await mock()).list() : call<OfficeTemplateCard[]>("GET", "/office-templates")),
  get: async (id: string, officeId?: string): Promise<OfficeTemplatePreview> =>
    MOCK ? (await mock()).get(id) : call<OfficeTemplatePreview>("GET", `/office-templates/${encodeURIComponent(id)}${q(officeId)}`),
  install: async (id: string, officeId?: string): Promise<OfficeTemplateInstall> =>
    MOCK ? (await mock()).install(id) : call<OfficeTemplateInstall>("POST", `/office-templates/${encodeURIComponent(id)}/install`, officeId ? { office_id: officeId } : {}),
  remove: async (id: string): Promise<void> => {
    if (MOCK) return (await mock()).remove(id);
    await call<unknown>("DELETE", `/office-templates/${encodeURIComponent(id)}`);
  },
};

/** Saves the template as a file on the person's computer. */
export function downloadTemplate(name: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: "application/json" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
