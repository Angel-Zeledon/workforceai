"use client";
import { useT } from "@/lib/i18n";
import { usePreferences } from "@/lib/preferences";
import { Btn, Empty } from "../ui";

/** /shop while the animated office is off: the objects only decorate the 3D office, so the shop opens with it. */
export function ShopOff() {
  const { t } = useT();
  const set = usePreferences((s) => s.set);
  return (
    <div data-testid="shop-off" className="h-full overflow-y-auto bg-bg">
      <div className="mx-auto w-full max-w-xl p-4 md:p-8">
        <Empty icon="bag" title={t("shop.off.title")}
          action={<Btn kind="primary" data-testid="shop-off-enable" onClick={() => set({ office3d: true })}>{t("shop.off.enable")}</Btn>}>
          {t("shop.off.body")}
        </Empty>
      </div>
    </div>
  );
}
