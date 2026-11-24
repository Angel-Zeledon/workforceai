import { OrgRedirect } from "@/components/nav/RoutedPages";

/** The old "Agentes y oficinas" address: `?tab=` decides whether it lands on Agentes or on Oficinas. */
export default function Page() {
  return <OrgRedirect />;
}
