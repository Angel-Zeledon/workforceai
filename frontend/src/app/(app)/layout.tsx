import { AuthGate } from "@/components/auth/AuthScreen";
import { AppShell } from "@/components/nav/AppShell";
import { AppSplash } from "@/components/nav/AppSplash";

export default function AppLayout({ children }: { children: React.ReactNode }) {
  return (
    <>
      <AppSplash />
      <AuthGate>
        <AppShell>{children}</AppShell>
      </AuthGate>
    </>
  );
}
