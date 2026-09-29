# Ověření dokončení OTA

Datum: 2026-09-29. Gateway změny jsou lokální na `feat/gateway-foundation`;
serverové změny jsou sloučené do `main` a [nasazené jako 2.7.13](server-deployment-2.7.13.md).
[Nasazení 0.1.5 do VM](virtualbox-lab.md#ota-launcher-015) ověřilo skutečný
Ansible provisioning, podepsaný start, izolovaný rollback a webovou diagnostiku.
Po nasazení serveru prošlo také autentizované OTA API a skutečné stažení
podepsaného vydání updaterem z produkčního HTTPS repository.

## Provedené kontroly

| Kontrola | Výsledek |
|---|---|
| `go test -race ./...` | Prošlo; včetně durable state, drainu, restartu, token scope a backoffu |
| `make check` | Go vet a formátování prošly |
| ARM64 build agenta, launcheru, updateru a root helperu | Prošlo |
| `scripts/test-ota.sh` | Skutečný podepsaný agent naběhl; vadné vydání se vrátilo na původní |
| Storage guard Python testy | Prošly |
| Ansible `ota.yml --syntax-check` | Prošlo |
| Python publisher `py_compile` | Prošlo |
| Server `mvn verify` s Go contract testy | 337 testů, 0 selhání, 0 chyb, 0 přeskočených; exit 0 |
| Dodatečné `GatewayOtaTests` | 5 testů prošlo; zahrnuje falešný podpis a finální Go klient |
| Web `npm run lint` | Prošlo |
| Web `npm run build -- --webpack` | Produkční statický export prošel |
| Chromium nad lokálním exportem s testovacími daty | Plánování, pause, resume, cancel, audit, mobilní šířka 390 px; ADMIN nemá OTA panel |

Turbopack nedokázal v tomto prostředí otevřít pomocný port, proto byl build
ověřen přes Webpack. Při ukončování úplné serverové sady Surefire ukončil
pomalu končící testovací JVM po 30 sekundách; samotné testy i Maven skončily
úspěšně. Log obsahoval také background dotazy ze starších testovacích Spring
kontextů na již zrušené testovací databáze.

## Propojení serveru a agenta

Test používá skutečný Spring HTTP/WSS transport, testovací MariaDB a Go klienta
s OTA controllerem. Server vydá a hostuje podepsaný artefakt. Současný přenos PDF
je úmyslně zablokovaný; před jeho dokončením nesmí vzniknout pending selection
ani být povolena aktivace. Poté se dokončí přenos, vybere nová verze a odešle se
výsledek bootu. Serverová evidence tisku skončí jako SENT a PDF se předá právě
jednou, také při ztrátě odpovědi na potvrzení během drainu.

Potvrzení bootu v tomto contract testu provádí harness. Skutečné spuštění procesu,
readiness pipes, verzi, timeout, pád a rollback kontrolují samostatné procesní
testy launcheru a ARM64 kontejner. Jednorázové podpisové klíče z testů nejsou
produkční trust anchors.

## Provozní aktivace

Nasazené: server 2.7.13, ConfigMap pilotního veřejného klíče, publisher Secret
a launcher testovací VM. Pilotní binárka 0.1.5 je publikovaná. Zbývá zákaznický
podpisový klíč a release workflow gateway na GitHubu.
Gateway repozitář stále nemá remote; workflow tedy zatím neběželo v GitHub Actions.
Ansible OTA playbook prošel na testovací VM; na zákaznické bráně zatím spuštěný nebyl.

Fyzické odebrání napájení na konkrétní RPi/SD kartě a tisk na PC42E patří do
hardwarového pilotu. Postup konfigurace a kontrakt jsou v
[OTA implementaci](ota-implementation.md).
