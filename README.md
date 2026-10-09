# ایران 3x-ui + چند exit-node خارجی (GitHub)

## معماری

- **ایران (یک سرور)**: Docker با `3x-ui` + `tunnel-server`
  - پورت‌های کنترل `8787` تا `8799` برای اتصال agentهای خارجی
  - هر agent آنلاین → یک SOCKS محلی روی `127.0.0.1:3XXXX` (مثلاً 8787 → 38787)
  - در پنل 3x-ui برای هر سرور خارجی یک outbound از نوع SOCKS بساز با نام مثلاً `🇺🇸 United States 1`
  - inboundهای کاربر را در 3x-ui بساز (VLESS / VMess / ...)
  - با routing یا چند inbound می‌توانی برای هر کاربر از تمام سرورهای فعال کانفیگ بسازی و حجم را کنترل کنی

- **خارج (چند GitHub Action)**: فقط agent
  - هر `.yml` به یک پورت کنترل وصل می‌شود (`host:8787`, `host:8788`, ...)
  - ترافیک از ایران از طریق تونل به اینترنت خارج می‌رود (exit node)

## ایران – دیپلوی

```bash
cd iran
# TUNNEL_TOKEN را در .env یا پنل Runflare ست کن (حداقل ۳۲ کاراکتر)
docker compose up -d --build
```

پورت‌های `8787-8799` را روی فایروال/پنل باز کن.

لاگ tunnel-server را ببین تا بفهمی کدام control آنلاین است و SOCKS محلی‌اش چیست:

```
agent ONLINE control=8787 ... → local SOCKS 127.0.0.1:38787
```

### تنظیم 3x-ui

1. پنل را باز کن (پیش‌فرض پورت 2053).
2. برای هر سرور خارجی فعال یک **Outbound** از نوع SOCKS بساز:
   - Address: `127.0.0.1`
   - Port: `38787` (برای 8787)، `38788` (برای 8788) و ...
   - نام: `🇺🇸 United States 1` و مشابه
3. Inboundهای کاربر را بساز (هر پروتکلی که می‌خواهی).
4. در Routing یا با تگ outbound، ترافیک را به outbound مورد نظر بفرست.
5. برای مولتی‌کانفیگ: چند inbound بساز و هر کدام را به یک outbound وصل کن، یا از قابلیت subscription پنل استفاده کن.

## گیت‌هاب – چند فایل yml

Secrets مشترک:
- `TUNNEL_TOKEN` (همان ایران)
- `TUNNEL_ADDR` مثلاً `your-iran-ip:8787` (برای هر workflow متفاوت)
- `TUNNEL_TLS_SHA256` خالی بگذار

برای ۱۰ سرور:

- `1.yml` → secret یا env با `TUNNEL_ADDR=iran:8787`
- `2.yml` → `iran:8788`
- ...
- `13.yml` → `iran:8799`

هر workflow را جداگانه Run کن. concurrency جدا است.

workflow فعلی فقط agent را اجرا می‌کند (بدون Xray).

## جریان ترافیک

کلاینت → inbound 3x-ui (ایران) → outbound SOCKS محلی → tunnel → agent خارجی → اینترنت

حجم و کاربر کاملاً توسط 3x-ui کنترل می‌شود.
