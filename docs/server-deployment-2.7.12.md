# Nasazení serveru 2.7.12

Nasazeno 2026-09-29 přibližně 10:32 CEST na `https://cloud.stitkovac.app`.

## Vydání a kontrola

- [Implementační PR #339](https://github.com/kratochj/stitkovac-server/pull/339), sloučené s kompletní historií implementačních commitů.
- [Release 2.7.12](https://github.com/kratochj/stitkovac-server/releases/tag/v2.7.12).
- [Docker build](https://github.com/kratochj/stitkovac-server/actions/runs/36541773466): úspěšný, linux/amd64 a linux/arm64.
- [Deployment PR #340](https://github.com/kratochj/stitkovac-server/pull/340): zelené backendové i frontendové CI, sloučeno.
- Backendové CI implementace: 332 testů, 0 chyb, 0 selhání, 1 přeskočený.
  Přeskočený Go/Spring test vyžaduje sousední gateway checkout a byl úspěšně
  spuštěný lokálně při implementaci.
- Frontend: lint a standardní Next.js build v CI úspěšné.

## Produkční stav po nasazení

Kontext Kubernetes `microk8s`, namespace `stitkovac-2`, deployment
`stitkovac-server`. Proti živému manifestu se změnil pouze image tag.

- Image: `registry.kratochvil.eu/apps/stitkovac:2.7.12`.
- Digest běžícího podu: `sha256:0c4821444e1f1aa858aae731a845f42d9c2e640c6268fc652284bc506699c6e9`.
- Pod: `stitkovac-server-789c5c7c99-8nx9v`, `1/1 Running`, 0 restartů při ověření.
- Rollout úspěšně dokončený, původní pod ukončený.
- Liquibase `046-gateway-transport` a `047-gateway-diagnostics` úspěšné.
  Přidané tabulky `GATEWAY`, `GATEWAY_JOB`, `GATEWAY_EVENT` a nullable vazby tiskáren.
- Veřejné `/actuator/health`, `/actuator/health/readiness` a
  `/actuator/health/liveness`: HTTP 200, `UP`.
- `/gateways`: HTTP 200. `/api/gateway/v1/jobs` bez tokenu: 401;
  `/api/admin/gateways` bez přihlášení: 403.

Ověření v produkci nezahrnovalo registraci testovací brány, skutečný tisk
ani odeslání umělé chyby do Rollbaru. Relay používá existující serverovou
Rollbar konfiguraci; samostatný gateway projekt a jeho token zbývá oddělit.
Zdrojový `main` je po release workflow na `2.7.13-SNAPSHOT`.

## Návrat aplikace

Předchozí image je `2.7.11`. Případný rollback zachová přidané tabulky a aktuální
evidenci; databáze se nevrací ze starého snapshotu. Pokud se mezitím začnou
používat tiskárny typu GATEWAY, je nutné nejprve jejich provoz zastavit a vyřešit
nastavení tiskáren, protože 2.7.11 tento typ nezná.

```sh
kubectl --context microk8s -n stitkovac-2 set image deployment/stitkovac-server \
  stitkovac-server=registry.kratochvil.eu/apps/stitkovac:2.7.11
kubectl --context microk8s -n stitkovac-2 rollout status deployment/stitkovac-server
```

Po případném rollbacku upravit také verzovaný deployment manifest v serverovém
repozitáři. Tyto příkazy jsou servisní postup; během tohoto nasazení nebyly spuštěny.
