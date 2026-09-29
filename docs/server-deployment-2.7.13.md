# Nasazení serveru 2.7.13

Nasazeno 2026-09-29 přibližně 14:33 CEST na `https://cloud.stitkovac.app`.

## Vydání a kontrola

- [Implementační PR #341](https://github.com/kratochj/stitkovac-server/pull/341): sloučeno po zeleném backendovém a frontendovém CI.
- [Release 2.7.13](https://github.com/kratochj/stitkovac-server/releases/tag/v2.7.13).
- [Docker build](https://github.com/kratochj/stitkovac-server/actions/runs/36567480949): úspěšný, linux/amd64 a linux/arm64.
- [Deployment PR #342](https://github.com/kratochj/stitkovac-server/pull/342): sloučeno po zeleném backendovém a frontendovém CI.
- Implementační CI: 337 testů, 0 selhání, 0 chyb, 2 přeskočené testy propojení Go/Spring.
  Ty vyžadují sousední gateway checkout a prošly lokálně při implementaci.
- Frontend: lint a standardní produkční build úspěšné v obou PR.

## Produkční stav

Kubernetes kontext `microk8s`, namespace `stitkovac-2`, deployment
`stitkovac-server`. Strategický patch změnil image a přidal OTA konfiguraci;
stávající proměnné, probes, resources a bezpečnostní nastavení zůstaly zachované.

- Image: `registry.kratochvil.eu/apps/stitkovac:2.7.13`.
- Image index a imageID podu: `sha256:8409b8832cd87403cc95a97ded60df6721993b861f86ae8efc9616d1f270d569`.
- Pod: `stitkovac-server-6c9b9d7cf9-55scx`, `1/1 Running`, 0 restartů při ověření.
- Rollout dokončený, původní pod ukončený.
- Liquibase `048-gateway-ota` úspěšně aplikované; přidané OTA tabulky a capability brány.
- Veřejné health, readiness a liveness: HTTP 200, `UP`.
- `/actuator/info`: verze `2.7.13`; zdrojový `main` je po release workflow na `2.7.14-SNAPSHOT`.
- `/gateways`: HTTP 200. Nepřihlášené admin release/rollout API: 403;
  gateway OTA command API bez tokenu: 401.

## Pilotní OTA repository

Server má ConfigMap `stitkovac-gateway-release-keys` s veřejným klíčem
`virtualbox-lab-20260929`, který odpovídá testovací VM. Obsah je verzovaný
v serverovém `docs/k8s/gateway-release-keys.yaml`. Soukromý podpisový klíč
zůstává na Macu, server jej nedostal. Tento klíč je určený pro pilot;
zákaznický provoz potřebuje samostatné produkční trust anchors.

Secret `stitkovac-gateway-release-publisher` obsahuje nový náhodný scoped token,
který umožňuje pouze publikovat vydání. Soukromá lokální kopie je v ignorovaném
`.local/releases/publisher-token` tohoto projektu s oprávněním 0600.
Token ani jeho hodnota nejsou součástí repozitáře nebo tohoto záznamu.

Po výslovném schválení uživatelem je publikovaný stejný podepsaný balíček
**gateway 0.1.5**, který běží v testovací VM:

- Platforma: `linux-arm64`, launcher protokol 2, velikost 20 770 430 bajtů.
- SHA-256: `cf0e9e0fa0b6730c8f8b2d338ed6a90ef215699834781defed87732efc311d9d`.
- [Podepsaný manifest](https://cloud.stitkovac.app/releases/0.1.5/linux-arm64/manifest.json) odpovídá lokálnímu manifestu bajt po bajtu.
- Skutečný ARM64 updater ve VM stáhl veřejný artefakt přes HTTPS/Cloudflare,
  ověřil podpis, velikost, SHA-256 a běžící verzi binárky.
- Test použil oddělený dočasný adresář na `/data`, po skončení odstraněný.
  Aktivní release selection, konfigurace a databáze běžící brány se nezměnily.

VM se po serverovém rollout znovu připojila. Autentizované OTA command API
vrátilo HTTP 200 s prázdnou frontou; token při ověření neopustil VM.
Chromium znovu ověřilo přihlášení, OTA readiness, cloudové spojení, TCP dostupnost
simulované tiskárny, historii a mobilní zobrazení.

**Nebyl vytvořen žádný gateway rollout.** Brána už běží na 0.1.5 a běžná
aktualizace vyžaduje vyšší verzi. Vzdálený přechod mezi dvěma verzemi nebyl
součástí tohoto produkčního ověření. Podpisový workflow gateway zatím nemá
vzdálený GitHub repozitář; publikování pilotu proběhlo ručně scoped publisherem.

## Návrat aplikace

Případný návrat serveru na 2.7.12 zachovává přidané tabulky i gateway tokeny.
Databázi nevracet ze starého snapshotu. Pokud už mezitím někdo zahájí OTA
rollout, nejprve jej pozastavit a vyřešit již aktivující brány, protože starý
server jejich OTA výsledky nepřijme.

```sh
kubectl --context microk8s -n stitkovac-2 set image deployment/stitkovac-server \
  stitkovac-server=registry.kratochvil.eu/apps/stitkovac:2.7.12
kubectl --context microk8s -n stitkovac-2 rollout status deployment/stitkovac-server
```

Po případném rollbacku upravit i verzovaný deployment manifest. Tyto příkazy
jsou servisní postup a během nasazení nebyly spuštěné.
