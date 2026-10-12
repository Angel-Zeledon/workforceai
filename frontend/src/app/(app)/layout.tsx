import { AuthGate } from "@/components/auth/AuthScreen";
import { AppShell } from "@/components/nav/AppShell";

export default function AppLayout({ children }: { children: React.ReactNode }) {
  return (
    <AuthGate>
      <AppShell>{children}</AppShell>
    </AuthGate>
  );
}
