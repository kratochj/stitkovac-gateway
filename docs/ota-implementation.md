# Aplikační OTA: implementace

## Hotový základ

`internal/update` ověřuje Ed25519 podpis přes přesné bajty manifestu oddělené
podpisovou doménou. Manifest váže stabilní číselnou verzi, platformu, velikost,
SHA-256 a protokol launcheru. Maximální manifest má 16 KiB, binární artefakt
128 MiB. Agent obsahuje lokální web, proto tvoří jeden aktualizační artefakt.
Důvěryhodné veřejné klíče se předávají z připraveného OS; privátní klíč nepatří
na bránu ani do serveru. Distribuce a správa skutečných klíčů zbývá k nasazení.

Úložiště vydání musí být předem vytvořené jako soukromý adresář na `/data`.
Instalace probíhá do dočasného adresáře. Po ověření délky a checksumu a po fsync
souborů/adresářů se celý adresář atomicky zveřejní pod verzí. Existující vydání
se nepřepisuje. Nedokončené adresáře nemohou být vybrány ke spuštění.

Samostatný atomický `selection.json` obsahuje aktivní, předchozí a požadovanou
verzi a příznak zkušebního startu. Požadavek nemění běžící proces. Před startem
kandidáta se trvale označí trial. Příští start bez potvrzení vrací předchozí
verzi a zaznamená neúspěšnou verzi. Poškozený kandidát se odmítne a původní verze
zůstane aktivní. Poškozený výběrový manifest vyžaduje servisní opravu, nevytváří
se automaticky nová identita nebo tisková databáze.

Rollback pracuje pouze s aplikační verzí. Tisková databáze, DHCP rezervace
ani diagnostika se nezálohují a neobnovují v rámci OTA. Každé spuštění znovu
ověřuje podpis i checksum binárky. Downgrade běžným požadavkem je zakázaný.

## Launcher a servisní nástroje

`gateway-launcher` drží procesní zámek a spouští ověřenou verzi jako podproces.
Pád před readiness, chybná odpověď nebo timeout vrátí předchozí verzi. Při
ukončení launcher předá SIGTERM celé skupině agenta a poskytne 20 s pro doběhnutí;
potom zbývající procesy ukončí. systemd zůstává vnějším dohledem služby.

Agent potvrzuje připravenost přes zděděnou pipe až po otevření a obnově DB,
navázání HTTPS listeneru a spuštění DHCP na správném rozhraní. Launcher trvale
potvrdí verzi a druhou pipe povolí cloudové úlohy. Internet není součástí health
checku. Agent lze nadále spustit samostatně bez launcheru.

`gateway-update` nabízí lokální příkazy `stage`, `initialize`, `request` a
`status`. Přijímá explicitní `--releases`, `--keys`, `--version`, případně
`--manifest` a `--artifact`. `stage` neaktivuje proces, `request` plánuje verzi
na příští start launcheru. Nástroj nikdy nevytváří chybějící datový oddíl.

Veřejné klíče mají JSON formát mapy identifikátor → base64 Ed25519 public key.
Podepsaná obálka má `keyId`, `payload` (base64 přesných bajtů JSON manifestu) a
`signature` (base64). Podpis pokrývá ASCII `stitkovac-gateway-release-v1`, jeden
nulový bajt a přesné bajty payloadu. Manifest obsahuje `schema: 1`, `version`,
`platform` (pro Pi `linux-arm64`), `size`, `sha256` a `launcherProtocol: 1`.
Verze v manifestu musí odpovídat `make VERSION=... arm64`; launcher porovnává
verzi ohlášenou agentem. Privátní podpisový klíč se nástrojům brány nepředává.

Build, `.deb` a Ansible již obsahují launcher i servisní updater. Výchozí unit
zatím dál spouští původního agenta: produkční přepnutí na launcher vyžaduje
provisioning důvěryhodných klíčů, prvního podepsaného vydání a oprávnění k
adresáři releases. Obsluha přes server, stahování a automatické odložení
aktivace do konce tisku ještě nejsou propojené. `request` se proto používá
pouze v servisním okně; nesmí být spojený s neřízeným restartem při tisku.

## Ověření

Automatické testy pokrývají neplatné podpisy, klíče, platformu, protokol,
checksum, zkrácené i prodloužené soubory, symlinky, pokusy o přepsání vydání,
poškozený výběrový manifest, nedokončenou instalaci, potvrzení i obnovu
nepotvrzeného startu. Procesní testy ověřují návrat po pádu, timeoutu a chybné
readiness, vyloučení druhého launcheru a povolení práce až po potvrzení verze. Jde o softwarové testy; odolnost ext4/SD při odebrání
napájení musí potvrdit pilot na konkrétním hardware.

## Navazující části

- Omezené HTTPS stahování z důvěryhodného zdroje a podepisovací release pipeline.
- Serverové OTA příkazy přes WSS, audit, stavové hlášení a superadmin rollout.
- Dokončení rozpracovaného tisku před aktivací, retence vydání a kontrola místa.
- Provisioning klíčů, launcheru a celého read-only image; hardwarové power-cut testy.

Dokud nejsou tyto části propojené a ověřené, OTA není zákaznická funkce.
