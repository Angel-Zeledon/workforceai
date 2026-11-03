import { redirect } from "next/navigation";
import { LANDING_URL } from "@/lib/config";

/** There is one landing, the public site: the old in-app landing at /welcome now sends there (old links keep working). */
export default function WelcomePage() {
  redirect(LANDING_URL);
}
