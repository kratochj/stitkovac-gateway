# Stav implementace

Aktualizováno 2026-09-29. První celek je funkční základ agenta s lokální
administrací a testovaným cloudovým transportem. Nejde o dokončenou zákaznickou
instalaci a změny zatím nejsou nasazené ani odeslané do vzdáleného repozitáře.

## Hotový základ

| Oblast | Implementace a ověření |
|---|---|
| Identita a přístupy | Explicitní init, privátní DB, Argon2id heslo, unikátní certifikát, procesní zámek |
| Persistence | SQLite WAL/FULL, odmítnutí chybějícího úložiště, obnova po ukončení procesu bez cleanupu |
| DHCP | MAC rezervace před OFFER, lease před ACK, RELEASE bez ztráty rezervace, DECLINE karanténa |
| Servisní web | HTTPS login/logout, CSRF/Origin/Host ochrany, limit přihlašování, přehled rezervací |
| Cloud | WSS handshake, heartbeat, reconnect, událostmi spouštěná synchronizace, HTTPS transport bez redirectů |
| Tisk | Ověření checksumu, journal před TCP zápisem, max. čtyři endpointy, detekce nejistého výsledku |
| Chyby | Oddělená omezená SQLite fronta, allowlist bez raw errors, opakované předání serverovému relay |
| Distribuce | ARM64 cross-build, systemd unit se storage guardem, bootstrap/diagnostika Ansible, `.deb` recept, CI |

## Navazující implementační celky

1. **Server:** gateway registrace/tokeny, session fencing, gateway dispatch a outbox,
   claim/start/result, bezpečný cursor, Rollbar relay a superadmin přehled.
   Implementované integrační testy používají simulovaný server, ne Spring backend.
2. **Síťová administrace:** NetworkManager helper, změna Wi-Fi s rollbackem,
   servisní AP, GPIO tlačítko, trvalé síťové profily a firewall provisioning.
3. **Správa tiskáren:** pojmenování a přiřazení endpointů, synchronizace konfigurace,
   ruční řešení konfliktů rezervací, nejistých úloh a výměny tiskárny.
   Přidělená DHCP adresa zatím není plnohodnotná serverová registrace tiskárny.
4. **Provozní dokončení:** retence tiskových dokumentů a historie, servisní změny
   přístupů, bezpečné aktualizace/rollback, bootstrap celého OS image a panic recovery
   s původním místem chyby. Běžné transportní chyby už mají bezpečná hlášení.
5. **Pilot:** ARM64 runtime, PC42E, MAC/IP conflict detection, DHCP interoperabilita,
   read-only image, reálné odebrání napájení a měření latence. Bez Raspberry Pi
   a tiskárny nelze tato ověření nahradit testem na macOS.

Při chybě dnešní v1 synchronizace klient znovu připojí WSS a požádá o aktuální
frontu. Detailní rozlišení neplatného jednotlivého jobu oproti výpadku transportu
se doplní společně se serverovým kontraktem. Hardware se zatím neaktivuje pro
zákaznický provoz; žádná chybějící část není považovaná za automaticky hotovou.
