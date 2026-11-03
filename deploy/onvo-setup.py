#!/usr/bin/env python3
"""Creates in ONVO Pay the products and recurring prices of WorkforceAI's plans (docs/architecture/billing-onvo.md).

    ONVO_SECRET_KEY=onvo_test_secret_key_... python3 deploy/onvo-setup.py

Safe to run again: a price is recognised by its nickname (workforceai_pro_monthly, ...) and never created twice.
Prints the ONVO_PRICE_* lines for deploy/vps/.env. Standard library only.

Prices (USD, two months free when paid yearly): Pro 29 / 290, Max 99 / 990.
The webhook is registered in the ONVO dashboard (Developers > Webhooks), not here: point it at
https://app.workforceai.es/api/v1/billing/onvo/webhook and copy its secret into ONVO_WEBHOOK_SECRET.
"""
import json
import os
import sys
import urllib.error
import urllib.request

API = os.environ.get("ONVO_API_BASE", "https://api.onvopay.com").rstrip("/")
KEY = os.environ.get("ONVO_SECRET_KEY", "")

PLANS = [
    # plan, product name, description, monthly cents, yearly cents
    ("pro", "WorkforceAI Pro", "Para un equipo pequeño: 12 USD de IA al mes, 5 proyectos activos, 10 personas, 10 GB.", 2900, 29000),
    ("max", "WorkforceAI Max", "Para varios equipos: 45 USD de IA al mes, 20 proyectos activos, 50 personas, 50 GB.", 9900, 99000),
]


def call(method, path, body=None):
    req = urllib.request.Request(API + path, method=method, data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Authorization": "Bearer " + KEY, "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            raw = r.read()
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        sys.exit(f"ONVO {method} {path}: {e.code} {e.read().decode(errors='replace')[:500]}")


def items(resp):
    """List answers come as {"data": [...]} or as a bare list."""
    if isinstance(resp, list):
        return resp
    return resp.get("data", []) if isinstance(resp, dict) else []


def main():
    if not (KEY.startswith("onvo_test_secret_key_") or KEY.startswith("onvo_live_secret_key_")):
        sys.exit("set ONVO_SECRET_KEY (onvo_test_secret_key_... first)")
    print("LIVE mode: this creates real products and prices." if KEY.startswith("onvo_live_") else "Test mode.")
    prices = {p.get("nickname"): p for p in items(call("GET", "/v1/prices?limit=100")) if isinstance(p, dict)}
    products = {p.get("name"): p for p in items(call("GET", "/v1/products?limit=100")) if isinstance(p, dict)}
    env = []
    for plan, name, desc, monthly, yearly in PLANS:
        product = products.get(name)
        if product:
            print(f"product {name}: exists {product['id']}")
        else:
            product = call("POST", "/v1/products", {"name": name, "description": desc, "isActive": True})
            print(f"product {name}: created {product['id']}")
        for period, interval, cents in (("monthly", "month", monthly), ("yearly", "year", yearly)):
            nick = f"workforceai_{plan}_{period}"
            price = prices.get(nick)
            if price:
                print(f"price {nick}: exists {price['id']}")
            else:
                price = call("POST", "/v1/prices", {"productId": product["id"], "unitAmount": cents, "currency": "USD",
                                                    "type": "recurring", "nickname": nick,
                                                    "recurring": {"interval": interval, "intervalCount": 1}})
                print(f"price {nick}: created {price['id']}")
            env.append(f"ONVO_PRICE_{plan.upper()}_{period.upper()}={price['id']}")
    print("\nFor deploy/vps/.env:")
    print("\n".join(env))
    print("\nWebhook (ONVO dashboard > Developers > Webhooks): https://app.workforceai.es/api/v1/billing/onvo/webhook")
    print("then ONVO_WEBHOOK_SECRET=<its secret>, plus ONVO_SECRET_KEY, ONVO_PUBLISHABLE_KEY and PLANS=on.")


if __name__ == "__main__":
    main()
