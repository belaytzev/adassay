# adfilter

Adblock для AI-агентов: фильтр рекламных и маркетинговых вставок из текста, который потребляют агенты.

## Деплой сервера общей базы

```sh
docker build -t ghcr.io/belaytzev/adfilter-server:latest .
kubectl apply -f deploy/k8s/
```

Наружу сервис публикуется через Cloudflare Tunnel, а не прямым пробросом порта:
Ingress из `deploy/k8s/ingress.yaml` остаётся внутрикластерным, а tunnel-под ходит
в Service `adfilter`. Отсюда же следует настройка `ADFILTER_TRUSTED_PROXIES` —
в ней перечисляются адреса или CIDR туннеля, чьему заголовку `CF-Connecting-IP`
сервер верит при подсчёте лимитов записи. Для любого другого пира заголовок
подконтролен клиенту, поэтому игнорируется.
