// SPDX-License-Identifier: AGPL-3.0-or-later
// Local example. Use fixture data only; never embed credentials in source.
export const meta = {
  apiVersion: 1,
  key: "example-plugin-key",
  name: "Example ingress transform",
  version: "1.0.0",
  author: { name: "Community developer" },
  configSchema: { type: "object", properties: {} },
};

// This runs before upstream account selection. The host controls scope/order.
// Add supported json.patch effects here; model/stream must stay unchanged.
export function ingress(ctx, request) {
  return { action: "continue" };
}
