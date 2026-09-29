# Stav implementace

Aktualizováno 2026-09-29. Serverová podpora bran je vydaná a nasazená v **2.7.13**
na `cloud.stitkovac.app`. Agent má lokální administraci, cloudový transport
a dokončenou softwarovou implementaci aplikačního OTA. Serverová OTA část je
nasazená v produkci s veřejným klíčem testovací brány. Nejde zatím o dokončenou
zákaznickou instalaci.
Gateway změny jsou commitované lokálně na `feat/gateway-foundation`; projekt
zatím nemá nastavený vzdálený Git repozitář.

## Hotový základ

| Oblast | Implementace a ověření |
|---|---|
| Identita a přístupy | Explicitní init, privátní DB, Argon2id heslo, unikátní certifikát, procesní zámek |
| Persistence | SQLite WAL/FULL, odmítnutí chybějícího úložiště, obnova po ukončení procesu bez cleanupu |
| DHCP | MAC rezervace před OFFER, lease před ACK, RELEASE bez ztráty rezervace, DECLINE karanténa |
| Servisní web | HTTPS login/logout, CSRF/Origin/Host ochrany, limit přihlašování, přehled rezervací, nastavení adresy serveru a tokenu, stav WSS připojení |
| Síťová administrace | Root NetworkManager helper, Wi-Fi s rollbackem, servisní AP, GPIO, trvalé profily, oddělené DHCP a firewall provisioning; software ověřen, rádiová část čeká na RPi pilot |
| Diagnostika tisku | Historie po 50 pokusech, filtr stavu, časy a potvrzení serverem, upozornění na UNKNOWN, TCP test aktivní rezervace bez odeslání dat a bez souběhu s tiskem |
| Cloud | WSS handshake, heartbeat, reconnect, událostmi spouštěná synchronizace, HTTPS transport bez redirectů |
| Tisk | Ověření checksumu, journal před TCP zápisem, max. čtyři endpointy, detekce nejistého výsledku |
| Chyby | Oddělená omezená SQLite fronta, allowlist bez raw errors, opakované předání serverovému relay |
| Server | Registrace, tokeny, session fencing, samostatná tisková fronta, WSS/HTTP API a Rollbar relay |
| Web serveru | Registrace a správa tokenů, superadmin přehled, přiřazení brány a MAC k tiskárně |
| OTA | WSS příkazy, podepsaný hosting, superadmin rollout/audit, dokončení aktivního tisku, readiness/rollback, retence, Ansible trust anchors a podpisová CI |
| Distribuce | ARM64 build všech nástrojů, systemd storage guard, Ansible bootstrap, sestavený a rozbalením ověřený `.deb`, ARM64 procesní test |
| VirtualBox laboratoř | Debian ARM64 s read-only systémem, simulovaný WSS server, DHCP/TCP tiskárna, ověřený štítek a účtenka, obnova po tvrdém vypnutí VM |

Podrobnosti: [nasazení serveru](server-deployment-2.7.13.md),
[OTA implementace a produkční aktivace](ota-implementation.md).
Testovací build a jeho omezení: [VirtualBox laboratoř](virtualbox-lab.md).
Agent **0.1.5** je nasazený v testovací VM přes podepsaný OTA launcher
s laboratorním klíčem. [Síťová administrace a instalační postup](network-administration.md).
Předchozí celek: [historie a diagnostika tisku](print-diagnostics.md); současná VM
je aktualizovaná při zachování nastaveného serveru a tokenu.

## Navazující implementační celky

1. **Aktivace OTA v provozu:** server 2.7.13 a pilotní launcher jsou nasazené.
   Pilotní 0.1.5 je publikované. Zbývá vzdálený gateway repozitář, produkční
   podpisové klíče, chráněné CI prostředí a provisioning zákaznických zařízení.
2. **Správa tiskáren:** automatická synchronizace DHCP rezervací a konfigurace,
   ruční řešení konfliktů rezervací, nejistých úloh a výměny tiskárny.
   Pojmenování a přiřazení brány/MAC/IP je nyní dostupné ve webu serveru ručně.
   Přidělená DHCP adresa sama ještě netvoří serverovou registraci tiskárny.
3. **Provozní dokončení:** retence tiskových dokumentů a historie, servisní změny
   přístupů, bootstrap celého OS image a panic recovery
   s původním místem chyby. Běžné transportní chyby už mají bezpečná hlášení.
4. **Pilot:** produkční end-to-end tisk, samostatný Rollbar projekt/token,
   kompatibilita nainstalovaných klientů, mezirepliková latence, PC42E,
   MAC/IP conflict detection, DHCP interoperabilita,
   read-only image, reálné odebrání napájení a měření latence. Bez Raspberry Pi
   a tiskárny nelze tato ověření nahradit testem na macOS ani v ARM64 kontejneru.

Při chybě dnešní v1 synchronizace klient znovu připojí WSS a požádá o aktuální
frontu. Detailní rozlišení neplatného jednotlivého jobu oproti výpadku transportu
zůstává k doplnění. Hardware se zatím neaktivuje pro
zákaznický provoz; žádná chybějící část není považovaná za automaticky hotovou.
