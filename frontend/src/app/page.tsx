import { Shell } from "@/components/Shell";
import { AuthGate } from "@/components/auth/AuthScreen";

export default function Page() {
  return (
    <AuthGate>
      <Shell />
    </AuthGate>
  );
}
