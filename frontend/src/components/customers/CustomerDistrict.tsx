"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { toast } from "@/lib/toast";
import { customersApi, type CustomerInput, type District } from "@/lib/customersApi";
import { CUSTOMER_STATUSES, customerColor, normalizeStatus, sortDistrict, STATUS_COLOR } from "@/lib/customersLogic";
import { Btn, Card, Empty, Loading } from "../ui";
import { CustomerForm } from "./CustomerForm";
import { CustomerView, StatusPill } from "./CustomerView";
import { DistrictMap } from "./DistrictMap";

/**
 * The customer district (B12): the firm sees its customers as a neighbourhood of buildings, the most urgent first.
 * Clicking a building opens that customer's scoped view (its clones, projects and requests).
 */
export function CustomerDistrict() {
  const { t } = useT();
  const [data, setData] = useState<District | null>(null);
  const [err, setErr] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const load = useCallback(() => {
    setErr(false);
    customersApi.district().then(setData).catch(() => setErr(true));
  }, []);
  useEffect(load, [load]);

  if (open) return <CustomerView id={open} onBack={() => { setOpen(null); load(); }} onChanged={load} />;

  const items = data ? sortDistrict(data.customers) : [];
  const create = async (v: CustomerInput) => {
    const c = await customersApi.create(v);
    setCreating(false); toast(t("customers.toast.created")); load(); setOpen(c.id);
  };
  const counts = Object.fromEntries(CUSTOMER_STATUSES.map((s) => [s, items.filter((i) => normalizeStatus(i.status) === s).length]));

  return (
    <div data-testid="customers-district" className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="font-display text-lg font-semibold text-ink">{t("customers.title")}</h2>
          <p className="mt-0.5 max-w-2xl text-xs text-mute">{t("customers.help")}</p>
        </div>
        <Btn data-testid="customers-new" kind="primary" onClick={() => setCreating((v) => !v)}>{t("customers.new")}</Btn>
      </div>
      {creating && <Card title={t("customers.new")}><CustomerForm onSave={create} onCancel={() => setCreating(false)} /></Card>}
      {err ? <Empty action={<Btn onClick={load}>{t("customers.retry")}</Btn>}>{t("customers.error.load")}</Empty>
        : !data ? <Loading rows={3} />
        : !items.length ? <Empty action={<Btn kind="primary" onClick={() => setCreating(true)}>{t("customers.new")}</Btn>}>{t("customers.empty")}</Empty>
        : (
          <>
            <div className="flex flex-wrap gap-3 text-[11px] text-ink2" aria-label={t("customers.legend")}>
              {CUSTOMER_STATUSES.map((s) => (
                <span key={s} className="inline-flex items-center gap-1.5">
                  <span aria-hidden className="inline-block h-2.5 w-2.5 rounded-full" style={{ background: STATUS_COLOR[s] }} />
                  {t(`customers.status.${s}`)} ({counts[s]})
                </span>
              ))}
            </div>
            <Card><DistrictMap items={items} onOpen={setOpen} /></Card>
            <Card title={t("customers.list.title", { count: items.length })}>
              <ul className="grid gap-2 md:grid-cols-2">
                {items.map((it) => (
                  <li key={it.customer.id}>
                    <button data-testid={`customers-row-${it.customer.id}`} onClick={() => setOpen(it.customer.id)}
                      className="w-full rounded-md border border-line bg-panel2 p-3 text-left transition hover:border-line-strong">
                      <div className="flex items-center justify-between gap-2">
                        <span className="flex min-w-0 items-center gap-2 text-[13px] font-semibold text-ink">
                          <span aria-hidden className="inline-block h-3 w-3 shrink-0 rounded-sm" style={{ background: customerColor(it.customer.color, it.customer.id) }} />
                          <span className="truncate">{it.customer.name}</span>
                        </span>
                        <StatusPill status={it.status} />
                      </div>
                      <div className="mt-1.5 text-[11px] text-mute">
                        {t("customers.row.summary", { clones: it.assignments, projects: it.projects_active, approvals: it.approvals_waiting })}
                      </div>
                    </button>
                  </li>
                ))}
              </ul>
            </Card>
          </>
        )}
    </div>
  );
}
