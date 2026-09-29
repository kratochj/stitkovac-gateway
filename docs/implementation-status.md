# Stav implementace

Aktualizováno 2026-09-29. Agent má lokální administraci a cloudový transport. Sousední `stitkovac-server`
na větvi `feat/gateway-server` nově implementuje serverové napojení i správu bran. Nejde o dokončenou zákaznickou
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
| Server | Registrace, tokeny, session fencing, samostatná tisková fronta, WSS/HTTP API a Rollbar relay |
| Web serveru | Registrace a správa tokenů, superadmin přehled, přiřazení brány a MAC k tiskárně |
| Distribuce | ARM64 cross-build, systemd unit se storage guardem, bootstrap/diagnostika Ansible, `.deb` recept, CI |

## Navazující implementační celky

1. **Nasazení serveru:** nové endpointy jsou implementované a testované lokálně,
   zatím nejsou v produkci. Ověřit mezireplikovou latenci (záložní oznámení do 5 s),
   oprávnění skutečného Rollbar projektu a kompatibilitu nainstalovaných klientů.
2. **Síťová administrace:** NetworkManager helper, změna Wi-Fi s rollbackem,
   servisní AP, GPIO tlačítko, trvalé síťové profily a firewall provisioning.
3. **Správa tiskáren:** automatická synchronizace DHCP rezervací a konfigurace,
   ruční řešení konfliktů rezervací, nejistých úloh a výměny tiskárny.
   Pojmenování a přiřazení brány/MAC/IP je nyní dostupné ve webu serveru ručně.
   Přidělená DHCP adresa sama ještě netvoří serverovou registraci tiskárny.
4. **Provozní dokončení:** retence tiskových dokumentů a historie, servisní změny
   přístupů, bezpečné aktualizace/rollback, bootstrap celého OS image a panic recovery
   s původním místem chyby. Běžné transportní chyby už mají bezpečná hlášení.
5. **Pilot:** ARM64 runtime, PC42E, MAC/IP conflict detection, DHCP interoperabilita,
   read-only image, reálné odebrání napájení a měření latence. Bez Raspberry Pi
   a tiskárny nelze tato ověření nahradit testem na macOS.

Při chybě dnešní v1 synchronizace klient znovu připojí WSS a požádá o aktuální
frontu. Detailní rozlišení neplatného jednotlivého jobu oproti výpadku transportu
zůstává k doplnění. Hardware se zatím neaktivuje pro
zákaznický provoz; žádná chybějící část není považovaná za automaticky hotovou.
