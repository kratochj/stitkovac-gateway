# Správa tiskáren a provozní dokončení

Implementace připravená 29. 9. 2026 v gatewayi a serveru. Změny nejsou nasazené.
Server potřebuje migraci `049-gateway-operations`; nasazuje se před novým agentem.
Rozšíření se vyjednává přes `inventoryProtocol: 1` v odpovědi WSS `ready`.
Starší agent i laboratorní server mohou dál používat původní tiskový protokol.

## Synchronizace tiskáren

- SQLite rezervace mají trvalou monotónní revizi. DHCP změna nebo servisní zásah
  probudí synchronizaci; po reconnectu se posílá celý aktuální snapshot.
- `PUT /api/gateway/v1/inventory` vyžaduje token konkrétní brány a aktuální session.
  Server odvozuje organizaci z tokenu. Odmítá starší revizi i změněný obsah stejné revize.
- Samotný OFFER ještě nevytváří tiskárnu. Po zapsané DHCP lease server automaticky
  vytvoří logickou tiskárnu `GATEWAY`, TCP 9100, s názvem odvozeným z MAC.
  Automaticky ji nevolí jako výchozí. Tento krok nevysílá tisková data.
- Další synchronizace zachovávají uživatelský název a výchozí volbu, aktualizují IP
  pro danou MAC. Smazaná logická tiskárna se automaticky neobnoví.
- Lokální web ukazuje revizi a poslední potvrzenou revizi. Rozdíl znamená čekající
  změny, nikoli úspěšné připojení tiskárny. Serverový web v části Brány zobrazuje
  snapshot rezervací, jeho čas, posledních 100 úloh a servisních událostí.
- Název a výchozí tiskárna zůstávají v běžné serverové správě tiskáren. Samotná
  DHCP adresa ani úspěšné TCP spojení nepotvrzují kompatibilitu či papírový výtisk.

## Konflikt adresy a výměna tiskárny

V lokálním webu vyberte MAC, zadejte očekávanou současnou IP a požadovanou IP.
Před potvrzením odpojte konfliktní zařízení a vyřešte rozpracované úlohy.
Stejná IP zruší karanténu po fyzické kontrole; jiná volná IP v poolu změní rezervaci.
Původní IP zůstává trvale vyřazená z přidělování, aby ji nemohl převzít jiný klient
během platnosti staré lease. Nakonec obnovte DHCP na tiskárně.

Změna je transakční, kontroluje očekávanou IP, obsazenost, aktivní přenosy
a nejisté výsledky. Audit uchová původní a novou adresu. Rezervace se běžnou
retencí nemažou a nikdy se automaticky nepřidělují jiné MAC.

Fyzickou výměnu proveďte v serverovém webu Brány → Tiskárny a provoz brány.
Vyberte logickou tiskárnu a novou MAC s aktivní lease. Server odmítne výměnu,
pokud kterákoli z obou MAC má otevřenou nebo nevyřešenou úlohu. Zachová jméno
a výchozí volbu, uloží audit s uživatelem a použije nové přiřazení jen pro nové úlohy.
Běžná editace tiskárny také nesmí obejít blokaci rozpracovaného tisku.

Snapshoty starých úloh se nikdy nepřepisují. Změněná/karanténní rezervace zastaví
start; dosud nezahájený neplatný snapshot se uzavře jako EXPIRED. Nový tisk
objednejte samostatně. Automatická detekce libovolné ručně nastavené cizí IP
na ethernetu není součástí tohoto kroku; DHCP DECLINE a fyzická servisní kontrola
zůstávají důležité a skutečná síť se ověřuje v pilotu.

## Nejistý výsledek a ztracená potvrzení

Lokální Historie tisku nabízí u UNKNOWN dvě rozhodnutí: výtisk zkontrolován,
nebo pokus uzavřen bez výtisku. Formulář vyžaduje potvrzení fyzické kontroly
a vyprázdnění bufferu tiskárny. Rozhodnutí neznamená nový tisk ani změnu prodeje.

Rozhodnutí se nejprve trvale uloží v bráně. Dokud je server nepotvrdí, lokální
endpoint zůstává blokovaný. Server kontroluje session, identitu pokusu a stav
UNKNOWN; duplicitní stejné rozhodnutí je idempotentní, odlišné odmítne.
Původní stav UNKNOWN zůstává zachovaný vedle rozhodnutí a auditní stopy.

Při synchronizaci agent nejprve předá nepotvrzené výsledky, potom servisní
rozhodnutí a rezervace a až pak přijímá další práci. Pokud server přijal SENT,
ale odpověď se ztratila, agent zopakuje pouze výsledek. Dokument už nemusí být
ve frontě serveru. Stav STARTED na serveru bez odpovídajícího bezpečného lokálního
záznamu se změní na nejistý výsledek, nikdy na nový přenos dokumentu.

## Retence

| Data | Pravidlo |
|---|---|
| Dokument po potvrzení výsledku | Odstraní se i pro UNKNOWN; nejistota zůstává |
| Nepotvrzený dokument | Nejvýše 7 dní po expiraci lokálně, 7 dní od vytvoření na serveru |
| Podrobná historie uzavřeného pokusu | 30 dní; agent vyžaduje potvrzený výsledek |
| Vyřešený UNKNOWN na serveru | 30 dní od rozhodnutí i ukončení |
| Nevyřešený UNKNOWN / neukončený přenos | Blokace se neodstraňuje retencí |
| Deduplikační tombstone | Trvale; pouze identifikátory, na serveru i výsledek potřebný pro ACK |
| Servisní audit | 365 dní |
| DHCP rezervace a vyřazené IP | Trvale |

Úklid běží při startu agenta a poté každou hodinu; server má hodinový úklid.
Jde o logickou retenci v databázi, nikoli o záruku forenzního přemazání SD karty
nebo databázových záloh. Tombstones rostou s počtem úloh, ale nenesou dokumenty.
Retence nikdy nepotvrdí výsledek jménem serveru ani nepovolí opakování tisku.

## Přístupy a panic recovery

Lokální web mění servisní heslo po ověření současného hesla, CSRF a originu.
Změna atomicky uloží Argon2id hash a odhlásí všechny relace, včetně ochrany před
souběžným přihlášením starým heslem. Obnova z lokální servisní konzole používá
`gateway password --password-file /run/private-password`; agent musí být zastavený.
Příkaz drží stejný procesní zámek a nemění identitu, rezervace ani tiskové pokusy.

Token lze nadále odvolat/vyměnit na serveru a uložit v lokálním webu. Rotace
a odvolání jsou auditované; token se do auditu nedostává. Samostatný
`deploy/ansible/access.yml` je offline servisní postup pro image: mění místní heslo,
SSH veřejný klíč a heslo servisního AP. Vyžaduje zastavený agent, explicitní
maintenance a Vault. Po změně vrací root do read-only režimu; technik ověří nový
SSH přístup a teprve pak znovu spustí služby. Není to vzdálený cloudový shell.

Panic recovery chrání agentovy servisní goroutines, WSS pracovníky a HTTP handlery.
Stack se zachytí přímo v deferred recovery před jeho rozvinutím, včetně původního
souboru/funkce/řádku. Hodnota panicu se nezapisuje – může obsahovat tajná data.
Událost se synchronně uloží do oddělené diagnostické fronty a agent se ukončí
s chybou pro restart přes systemd/launcher. Při obnově se SENDING změní na UNKNOWN.
Doručení do Rollbaru využívá stávající serverový relay. Není-li datový disk
zapisovatelný, nelze garantovat uložení diagnostiky; tisk tím nezíská oprávnění pokračovat.

## Ověření a nasazení

Regrese pokrývají revize, izolaci organizací/sessions, ztracené ACK, fyzické
endpointy při výměně, ochranu proti opakování po retenci, ruční odblokování,
rotaci hesla a původní místo panicu. Součástí je test Go agent ↔ Spring ↔ MariaDB
a existující OTA kontrakt. Image má vlastní testy vstupů a Ansible syntax check.
Postup sestavení celého OS je v [OS image](os-image.md).

Po review nasadit nejprve server, následně připravit podepsaný agent release.
Tato implementace sama žádný server, VM ani zákaznickou bránu neaktualizuje.
Hardwarový pilot na RPi/PC42E, boot konkrétního připnutého image a fyzické power-cut
zkoušky zůstávají povinným ověřením před zákaznickým provozem.

### Výsledek ověření 29. 9. 2026

- Gateway: kompletní `go test -race ./...`, `go vet`, gofmt a Linux ARM64 build.
- Server: `mvn verify` dokončeno s exit code 0, 343 testů bez selhání a bez přeskočení.
  Při zavírání sdílených testovacích Spring kontextů Surefire po 30 s ukončil
  testovací JVM; log obsahuje chyby schedulerů proti již zastaveným testovacím DB.
  Nejde o selhání assertion ani nasazení.
- Web: ESLint, TypeScript a produkční export přes Webpack. Turbopack v místním
  prostředí nedokázal otevřít interní port; jeho build zde není označen za úspěšný.
- Chromium: skutečné HTTPS formuláře, čekající rozhodnutí, rotace hesla a mobilní
  rozvržení na izolované fixture bez přístupu k VM či cloudovým přihlašovacím údajům.
- Image: 3 testy vstupních pojistek a seed konfigurace, Python compile, syntax check
  playbooků image/network/access. Bez sestavení a bootu konkrétního RPi obrazu.
