# Historie a diagnostika tisku

Od agenta 0.1.3 je v lokální administraci dostupný přehled **Tiskové úlohy**
a u každé aktivní DHCP rezervace tlačítko **Ověřit spojení**.
Obě funkce vyžadují servisní přihlášení a fungují i bez internetu.

## Tiskové úlohy

Stránka `/jobs` čte skutečnou lokální evidenci pokusů. Zobrazuje nejvýše
50 záznamů od nejnověji převzatých, filtr podle stavu a odkaz na starší stránku.
Stránkování používá stabilní kurzor; nově příchozí úlohy neposouvají starší
stránky. Obsah PDF se nenačítá ani nezobrazuje.

| Stav | Význam |
|---|---|
| Převzato | Dokument čeká na povolení a zahájení přenosu. |
| Probíhá přenos | Brána zahájila odesílání tiskárně. |
| Data odeslána | Celý dokument byl předán přes TCP; fyzický výtisk tím není potvrzený. |
| Neodesláno | Přenos selhal před odesláním dat. |
| Nejistý výsledek | Část nebo celý dokument mohl být odeslán. Další start na stejnou IP/port zůstává blokovaný. |
| Vypršela platnost | Pokus vypršel před odesláním dokumentu. |

U dokončených stavů je vidět, zda server převzetí výsledku potvrdil. Ztráta
potvrzení neznamená nový tisk; brána znovu hlásí uložený výsledek.
Nejisté úlohy navíc vyvolají upozornění na hlavní stránce. Kontrola TCP spojení
jejich blokaci neruší. Ruční vyřešení nejistého výsledku a řízený nový tisk
je navazující funkce, která musí být koordinovaná se serverem.

Časy převzetí, změny stavu a potvrzení se zaznamenávají od této verze a zobrazují
v UTC. U dřívějších záznamů jsou neznámé časy označené **Nezaznamenáno**;
neodvozují se z platnosti pokusu ani z času aktualizace softwaru.
Historie patří fyzické bráně a zůstává zachovaná i po změně cloudového serveru.

## Ověření spojení

Test pracuje výhradně s vybranou MAC z evidence. IP načte z její rezervace;
prohlížeč nemůže zadat jiný cíl, port ani název hostitele. Adresa musí patřit
do nakonfigurovaného privátního DHCP poolu, mít aktivní lease a být bez konfliktu.

Brána otevře TCP port 9100 přes vyhrazené tiskové rozhraní a spojení hned zavře.
Neodesílá bajty, nevytváří tiskový pokus a nezkouší stav papíru nebo krytu.
Celý test má limit tří sekund. Používá stejný zámek fyzického endpointu jako
tisk; při probíhajícím přenosu se test odmítne. Opačně může právě zahájený
test pozdržet následující tisk nejvýše o svůj timeout.

Web dovoluje nejvýše jeden test současně a začátky testů alespoň dvě sekundy
od sebe. POST vyžaduje session, správný Origin a CSRF. Výsledek se uchovává jen
v přihlášené session, obsahuje čas kontroly a po odhlášení zanikne.
Nezpracované síťové chyby se neposílají do stránky ani do diagnostiky.

## Uložení a kompatibilita

Tabulka `attempt_history`, index stavu a tři SQLite triggery jsou doplňkem
existujícího journalu v1. Přidávají se v jedné transakci při otevření databáze;
nemění identitu, rezervace, dokumenty ani význam tiskových stavů.
Časové údaje se zapisují ve stejné transakci jako příslušný stav pokusu.

`user_version` zůstává 1, protože původní tabulky i jejich kontrakt jsou beze
změny. Předchozí agent je může dál číst a zapisovat; triggery zachytí i jeho
změny. Rollback aplikace proto nevyžaduje obnovu tiskové databáze.
Historie nemaže dokumenty, deduplikační záznamy ani nejisté pokusy.
Retence je stále samostatná navazující práce.

## Ověření

- Testy pokrývají přechody stavů, potvrzení, stránkování při souběžném příchodu
  úloh, filtry, upgrade původní databáze a zachování identity a dokumentů.
- HTTP testy ověřují přihlášení, CSRF/Origin, omezení četnosti, escapování ID,
  upozornění na nejistý výsledek a nepřítomnost obsahu PDF v HTML.
- Testy tiskového workeru ověřují povolené cíle, timeout, nulový zápis bajtů,
  absenci nového tiskového pokusu a vyloučení souběhu s tiskem.
- Ve VirtualBoxu prošla kontrola simulované tiskárny, prohlížení a filtrování
  existující historie i zobrazení na šířce 390 px. Po testu zůstaly cloudová
  konfigurace i seznam zachycených PDF beze změny.

Pro zopakování webového testu použij `scripts/virtualbox/smoke-browser.mjs
--diagnostics` s Chromium nastaveným podle [návodu laboratoře](virtualbox-lab.md).
Test nepřepíná cloud a kontroluje pouze známou simulovanou tiskárnu.
