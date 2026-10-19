"use client";
import { ApiAccessTokens } from "./ApiAccessTokens";
import { ApiAccessWebhooks } from "./ApiAccessWebhooks";

/** API tokens and outbound webhooks of the organization (owner/admin). Contract: docs/architecture/governance.md. */
export function ApiAccess() {
  return (
    <div className="space-y-5" data-testid="api-access">
      <ApiAccessTokens />
      <ApiAccessWebhooks />
    </div>
  );
}
