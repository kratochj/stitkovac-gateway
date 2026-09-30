# Stav implementace

Aktualizováno 2026-09-30. Serverová podpora bran je vydaná a nasazená v **2.7.13**
na `cloud.stitkovac.app`. Agent má lokální administraci, cloudový transport
a dokončenou softwarovou implementaci aplikačního OTA. Serverová OTA část je
nasazená v produkci s veřejným klíčem testovací brány. Nejde zatím o dokončenou
zákaznickou instalaci.
Gateway má vzdálený repozitář `kratochj/stitkovac-gateway`. Správa tiskáren a provozní
dokončení jsou připravené v pracovních větvích; dosud nejsou nasazené.

## Fyzický pilot Pi 3 B+

Uživatel potvrdil první start image 0.1.8, připojení k Wi-Fi a tisk několika
štítků na skutečné tiskárně. Dne 30. 9. byl na tomto Pi přes Ethernet nasazen
servisní upgrade **0.1.9** pro [administraci přes zákaznickou Wi-Fi](wifi-admin.md).
Launcher potvrdil aktivní 0.1.9 bez trialu; agent, pomocník a firewall běží,
root i boot zůstaly read-only a storage guard prošel. Uživatel následně zapnul
přístup přes zákaznickou Wi-Fi a potvrdil jeho funkčnost. Nejde o plošné nasazení
ani publikaci tohoto vydání na server. Ověření servisního AP a opakovaných
výpadků napájení na hardware zůstává samostatným krokem.

## Hotový základ

| Oblast | Implementace a ověření |
|---|---|
| Identita a přístupy | Explicitní init, privátní DB, Argon2id heslo, unikátní certifikát, procesní zámek |
| Persistence | SQLite WAL/FULL, odmítnutí chybějícího úložiště, obnova po ukončení procesu bez cleanupu |
| DHCP | MAC rezervace před OFFER, lease před ACK, RELEASE bez ztráty rezervace, DECLINE karanténa |
| Servisní web | HTTPS login/logout, CSRF/Origin/Host ochrany, limit přihlašování, přehled rezervací, nastavení adresy serveru a tokenu, stav WSS připojení |
| Síťová administrace | Root NetworkManager helper, Wi-Fi s rollbackem, servisní AP, GPIO, trvalé profily, oddělené DHCP a firewall provisioning; software ověřen, Wi-Fi uplink a Ethernet fungují na pilotním Pi; servisní AP čeká na hardwarové ověření |
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
   Pilotní 0.1.5 je publikované. Zbývají produkční
   podpisové klíče, chráněné CI prostředí a provisioning zákaznických zařízení.
2. **Správa tiskáren – implementováno, nenasazeno:** verzovaná DHCP synchronizace,
   automatická registrace po lease, servis konfliktů, potvrzované řešení UNKNOWN,
   výměna tiskárny a audit. [Kontrakt a ověření](printer-operations.md).
3. **Provozní dokončení – implementováno, nenasazeno:** retence dokumentů/historie
   s trvalými tombstones, změny servisních přístupů, panic recovery s původním
   stackem a [kompletní OS image 0.1.8 pro Pi 3 B+](os-image.md). Provisioning,
   inicializace podepsaného OTA a read-only druhý boot prošly v QEMU s 1 GB RAM.
   Fyzický první boot, Wi-Fi uplink a tisk na pilotním Pi už potvrdil uživatel;
   servisní AP a opakované výpadky napájení zůstávají k ověření.
4. **Pilot:** produkční end-to-end tisk, samostatný Rollbar projekt/token,
   kompatibilita nainstalovaných klientů, mezirepliková latence, PC42E,
   MAC/IP conflict detection, DHCP interoperabilita,
   read-only image, reálné odebrání napájení a měření latence. Bez Raspberry Pi
   a tiskárny nelze tato ověření nahradit testem na macOS ani v ARM64 kontejneru.

Při chybě dnešní v1 synchronizace klient znovu připojí WSS a požádá o aktuální
frontu. Detailní rozlišení neplatného jednotlivého jobu oproti výpadku transportu
zůstává k doplnění. Hardware se zatím neaktivuje pro
zákaznický provoz; žádná chybějící část není považovaná za automaticky hotovou.
