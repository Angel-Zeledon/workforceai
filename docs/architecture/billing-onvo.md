# Cobro de los planes con ONVO Pay

Los planes (Free, Pro, Max) y sus límites están en `approvals-notify.md` sección 3.1. Esto es cómo se pagan, con
ONVO Pay (Costa Rica, https://docs.onvopay.com). Código: `backend/internal/billing` (cliente HTTP mínimo de ONVO),
`backend/internal/api/billing.go`, `backend/cmd/server/billing.go`; pantalla `PlanBilling.tsx` y su formulario de pago
`OnvoPayDialog.tsx`.

## Principio

Nada cambia un plan porque el navegador o un webhook lo digan: antes de mover un plan el backend **lee la suscripción en
ONVO** y solo una `active` (o `trialing`) cuenta. Todo es idempotente: confirmar dos veces, un webhook repetido o el
trabajo periódico no cambian nada si ONVO no cambió. La tarjeta nunca pasa por nuestros servidores: la recibe el
formulario de ONVO.

## Flujo

1. **Comprar** (Ajustes → Plan y uso → "Mejorar a Pro", solo owner, `billing:manage`): `POST /api/v1/plan/subscribe
   {plan, period, name, email}`. El backend crea el cliente en ONVO (una vez por organización) y una suscripción
   `paymentBehavior: allow_incomplete` con `metadata {app: workforceai, org, plan, period}`, y la guarda como
   **pendiente** (`org_plans.provider_pending`). El plan no cambia todavía.
2. La pantalla abre el formulario de ONVO (`https://sdk.onvopay.com/sdk.js`,
   `onvo.pay({publicKey, subscriptionId, customerId, paymentType: "subscription"})`). Al `onSuccess` llama
   `POST /api/v1/plan/confirm {subscription_id}`; mientras ONVO confirma responde `409 not_paid` y la pantalla
   reintenta unos 30 s.
3. **Confirmar**: la suscripción debe ser la pendiente de esa organización (metadata `app` y `org`, mismo cliente) y
   estar `active`. Si había otra suscripción, **primero se cancela la anterior** (DELETE, o `cancelAt` ahora) y después
   cambia el plan: si algo falla a mitad, nunca quedan dos cobrándose; reintentar termina el cambio.
4. **Cambiar de plan** (Pro ↔ Max): el mismo flujo. Es inmediato: se cobra el precio completo del nuevo plan y lo que
   quedaba del periodo anterior **no se reembolsa** (decisión del dueño; ONVO no prorratea). La pantalla lo avisa antes.
5. **Cancelar**: `POST /api/v1/plan/cancel {resume: false}` pone `cancelAtPeriodEnd` en ONVO. El plan sigue hasta el
   final del periodo pagado; "No cancelar" lo deshace (`resume: true`). Pasar a Gratis desde el catálogo es cancelar.
6. **Cobros que fallan**: `past_due` mantiene el plan mientras ONVO reintenta (la pantalla lo avisa); `unpaid`,
   `canceled` o `incomplete_expired` → Free. Nada se borra: solo vuelven a aplicar los límites de Free.

Con ONVO encendido, `PUT /plan` (cambiar sin pagar) responde 403 aunque `PLAN_SELF_SERVE` esté encendido.

## Webhook y trabajo periódico

- `POST /api/v1/billing/onvo/webhook` (público). ONVO no firma: manda el secreto del endpoint en `X-Webhook-Secret`,
  que se compara en tiempo constante (401 si no coincide). De cada evento solo se toma el `subscriptionId`
  (`subscription.renewal.succeeded` / `.failed`); la suscripción se lee de ONVO y, si es de esta app, se sincroniza su
  organización. Un evento falso con el secreto correcto no puede dar un plan: ONVO decide. Eventos sin suscripción
  (`payment-intent.*`, `checkout-session.succeeded`) → 200 y se ignoran.
- ONVO no avisa cuando una suscripción se cancela al final del periodo ni cuando el primer pago llega después de cerrar
  el navegador: un trabajo cada 15 min (`BILLING_SYNC_JOB=off` lo apaga) sincroniza toda organización con suscripción
  o pendiente en ONVO.

## Configuración

Variables del backend (en el VPS, solo en `deploy/vps/.env`; el compose las pasa, apagadas por defecto). Con
`ONVO_SECRET_KEY` y algo de lo demás faltando, el backend no arranca.

| Variable | Valor |
|---|---|
| `ONVO_SECRET_KEY` | `onvo_test_secret_key_...` / `onvo_live_secret_key_...` |
| `ONVO_PUBLISHABLE_KEY` | `onvo_test_publishable_key_...` del mismo modo (el formulario de pago la usa) |
| `ONVO_WEBHOOK_SECRET` | el secreto del webhook en el panel de ONVO |
| `ONVO_PRICE_PRO_MONTHLY`, `ONVO_PRICE_MAX_MONTHLY` | ids de los precios mensuales (obligatorios) |
| `ONVO_PRICE_PRO_YEARLY`, `ONVO_PRICE_MAX_YEARLY` | anuales (opcionales: sin ellos solo se ofrece mensual) |
| `PLANS` | `on` (obligatorio con ONVO) |

Productos y precios (idempotente, por `nickname`):

```sh
ONVO_SECRET_KEY=onvo_test_secret_key_... python3 deploy/onvo-setup.py
```

Imprime las líneas `ONVO_PRICE_*`. El webhook se registra en el panel de ONVO (Desarrolladores → Webhooks) con la URL
`https://app.workforceai.es/api/v1/billing/onvo/webhook`.

## Probado en modo test (2026-11-02, entorno local)

Con la tarjeta de prueba `4242 4242 4242 4242`: comprar Pro (el formulario del SDK con `paymentType: "subscription"`
sí confirma una suscripción `allow_incomplete` y `/plan/confirm` mueve el plan), cancelar al final del periodo y "No
cancelar". En local el webhook no llega (ONVO no alcanza `localhost`); `/plan/confirm` y el trabajo de 15 min bastan.
Tarjetas de prueba: https://docs.onvopay.com/payments/testing (rechazo: `4000 0000 0000 0002`; 3DS: `4000 0000 0000 3220`).

## Lo que la documentación de ONVO no deja claro (probar en modo test antes de activar en vivo)

- Si `DELETE /v1/subscriptions/{id}` existe (si no, se usa `cancelAt` = ahora).
- La forma exacta de la suscripción al leerla (`customerId` o `customer`; `items[].priceId` o `items[].price.id`;
  `currentPeriodEnd` o `periodEnd`): el cliente acepta todas.
- Que el formulario del SDK con `paymentType: "subscription"` confirme una suscripción `allow_incomplete`.
- Cambiar la tarjeta de una suscripción activa (no hay portal de cliente en ONVO). Si un cobro falla (`past_due`), la
  pantalla ofrece "Pagar con otra tarjeta": una suscripción nueva del mismo plan que, al pagarse, reemplaza a la que
  falla (se cancela primero). Fuera de `past_due` no se puede volver a comprar el mismo plan.
