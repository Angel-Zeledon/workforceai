"use client";
import { useT } from "@/lib/i18n";
import { hasKey, tr } from "@/lib/i18n-core";
import { actionIsUnnamed, actionLabel } from "@/lib/actionLabel";

const lookup = (k: string) => (hasKey(k) ? tr(k) : undefined);

/** The name in words of an action ("Enviar correo", never `email.send`). Without a name it says "Acción del agente";
 *  the code is still one click away in "Ver detalle" for whoever needs it. */
export function useActionName() {
  useT(); // re-render on a language change
  return (code: string | null | undefined) => actionLabel(code, lookup);
}

export function ActionName({ code, className = "" }: { code: string | null | undefined; className?: string }) {
  const { t } = useT();
  const label = actionLabel(code, lookup);
  if (!code || !actionIsUnnamed(code, lookup)) return <span data-action={code ?? ""} className={className}>{label}</span>;
  return (
    <span data-action={code} className={className}>
      {label}
      <details className="inline-block align-baseline">
        <summary className="ml-1 inline cursor-pointer list-none text-[10.5px] font-medium text-accent hover:underline">{t("action.details")}</summary>
        <code className="ml-1 font-mono text-[10.5px] text-mute">{code}</code>
      </details>
    </span>
  );
}
