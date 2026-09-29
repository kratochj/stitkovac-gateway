# Aplikační OTA

## Rozsah implementace

Hotové je propojení serverových příkazů přes WSS, podepsaného HTTPS hostingu,
trvalého stavu, dokončení rozběhnutého tisku, launcheru, rollbacku a auditu.
Serverový SUPERADMIN vybírá konkrétní brány a servisní okno v části
**Tiskové brány → Aktualizace bran**. Lokální web brány ukazuje připravenost OTA
a poslední stav. Nejdříve ověřit vydání na jedné zkušební bráně a pak vytvořit
nasazení pro další vybrané brány. Publikování vydání samo nic neaktivuje.

Produkční aktivace vyžaduje vydat novou serverovou verzi, nastavit skutečné
podpisové klíče a publisher credential, připojit gateway repozitář ke GitHubu
s chráněným release prostředím a provisionovat brány. V této implementaci
nevznikl produkční klíč, nebyl nasazen server ani upravena zákaznická konfigurace
stávající VM. VM dál používá agenta 0.1.4 bez launcheru. Tyto provozní kroky
nejsou nahrazeny úspěšnými lokálními testy.

## Průběh nasazení

1. Server vytvoří samostatný příkaz pro každou vybranou bránu a přes WSS pošle
   `ota.available`. Agent získá příkaz přes gateway API. Při reconnectu se stav
   rekonciluje; agent frontu příkazů nepoluje.
2. Agent uloží příkaz a monotónní sekvenci do soukromého atomického
   `/data/gateway/ota-command.json`. Stav je svázaný s adresou serveru a neměnným ID
   zařízení, ne pouze s názvem verze. Rotace tokenu neztratí nedoručený výsledek.
3. Stáhne manifest a artefakt z pevně provisionovaného HTTPS originu. Během
   stahování může tisk pokračovat. Ověří Ed25519 podpis, platformu, stabilní
   verzi, velikost, SHA-256, protokol launcheru a volné místo.
4. Ohlásí `DRAINING`, zastaví cloudové pracovníky a počká na jejich ukončení.
   Již povolený TCP přenos má vlastní 15sekundový deadline a dokončí se i po
   zrušení WSS kontextu. Jeho výsledek se uloží před odchodem pracovníka.
   Celé čekání má limit 45 sekund. Při chybě aktivace tisk pokračuje.
5. Server v samostatném aktivačním grantu znovu ověří servisní okno a stav
   nasazení. Pozastavení či zrušení blokuje další granty. Již povolený restart
   se dokončí. U nejasného výsledku grant requestu brána konzervativně neaktivuje.
6. Agent atomicky vybere pending vydání a skončí kódem 75. Launcher tento kód
   přijme pouze s existujícím ověřeným pending vydáním. Sám přepne proces;
   nepoužívá `sudo`, `systemctl restart` ani vzdálený shell.
7. Launcher před spuštěním uloží příznak trial. Agent musí do 30 sekund otevřít
   a obnovit DB, spustit DHCP a HTTPS a potvrdit přes zděděnou pipe správnou verzi.
   Launcher trvale potvrdí výběr a druhou pipe povolí cloud. Internet není health
   check. Po startu agent ohlásí `SUCCEEDED`, nebo `ROLLED_BACK` po obnově.

Pád nebo odebrání napájení před potvrzením trial vrací předchozí vydání.
Pád mezi lokálním aktivačním záměrem a výběrem verze ohlásí přerušení.
Nepotvrzené výsledky se trvale uchovávají a znovu odešlou po připojení; timer
opakuje pouze neodeslaný konečný report. Selhání a rollback mají také očištěnou
událost `ota_update_failed` pro existující Rollbar relay.

Server má servisní okna do 24 hodin a nejvýše 30 dnů dopředu. Selhání brány
pozastaví další aktivace. Offline příkazy expirují; již aktivující se brána
zůstane čekat na vlastní výsledek. `COMPLETED` znamená ukončení všech příkazů,
ne automaticky úspěch všech zařízení. Audit rozlišuje každý výsledek.

## Podepsaná vydání a retence

Ed25519 podpis pokrývá přesné bajty JSON manifestu s doménou
`stitkovac-gateway-release-v1` následovanou nulovým bajtem. Veřejná mapa klíčů
má formát `keyId → base64 Ed25519 public key`. Obálka má `keyId`, `payload`
(base64 přesných JSON bajtů) a `signature` (base64). Manifest obsahuje:
`schema: 1`, stabilní `version`, `platform: linux-arm64`, `size`, `sha256`
a `launcherProtocol: 2`.

Launcher 2 přijímá také podepsané manifesty protokolu 1. Nové vzdálené nasazení
na serveru ale vyžaduje protokol 2; staré vydání bez OTA klienta by neumělo
potvrdit výsledek ani přijímat další příkazy. Aktualizace launcheru, root helperu,
trust anchors a OS jsou servisní operace, nikoli tento aplikační OTA kanál.

Manifest má nejvýše 16 KiB. Lokální updater přijímá artefakt do 128 MiB;
serverový hosting má záměrně limit **32 MiB**. Před stažením musí zbývat místo
na celý artefakt a dalších 64 MiB. Server ukládá neměnná vydání v MariaDB po
1MiB blocích; nepotřebuje další DNS, sdílený disk ani zvýšený databázový packet.

Pevné veřejné cesty:
`/releases/X.Y.Z/linux-arm64/manifest.json` a `.../gateway`.
Při HTTP 429/502/503/504 brána opakuje download s náhodně rozloženým backoffem
a společným limitem tří minut pro manifest i artefakt. Zaplněné download sloty
tak nevyvolají okamžité selhání celého rollout.
Stažení nepřenáší token brány, nepovoluje redirecty, credentials, query ani
libovolnou cestu ze serverového příkazu. Staging používá privátní dočasný adresář,
fsync a atomické zveřejnění. Existující verze se nepřepisuje.

Na bráně se ponechá aktivní, předchozí, případná pending a právě připravovaná
verze. Před dalším stažením se odstraní starší verze a přerušené staging adresáře
pod stejným instalačním zámkem. Serverová historická vydání zůstávají dostupná
pro audit a obnovu; kapacitu a zálohy je nutné počítat i s nimi.

`ROLLBACK` je explicitní samostatná akce pouze na uchované předchozí vydání.
Neobnovuje databázový snapshot, DHCP rezervace, konfiguraci ani tiskovou evidenci.
Migrace agenta proto musí zůstat zpětně kompatibilní s předchozím vydáním.
Neplatná selection metadata se neopravují automatickým vytvořením nové identity.

## Klíče a release pipeline

Privátní klíč je PKCS8 Ed25519 PEM s režimem 0600 mimo repozitář. Vytvořit jej
jednou na zabezpečené release stanici; jeho záloha patří mimo brány i server.

```sh
umask 077
openssl genpkey -algorithm Ed25519 -out /private/releases/signing-key.pem
make VERSION=X.Y.Z build arm64
bin/gateway-release --artifact bin/gateway-linux-arm64 --version X.Y.Z \
  --key-file /private/releases/signing-key.pem --key-id release-1 \
  --manifest-out /private/releases/manifest.json > /private/releases/public-keys.json
```

Nástroj odmítá symlink, přístupná soukromá data klíče a přepis manifestu. Výstup
obsahuje pouze veřejný klíč. Verze podepisovaného vydání musí odpovídat verzi
zabudované přes `make VERSION=...`; startup handshake tuto shodu znovu ověřuje.

Workflow `.github/workflows/release.yml` reaguje na stabilní tag `vX.Y.Z`.
Oddělený build job nemá podpisová tajemství. Podpis a publikování běží až v
chráněném GitHub environment **gateway-release**. Nastavit ochranu tagů,
přístup k tomuto prostředí a kontrolu změn workflow. Environment obsahuje:

| Druh | Název | Obsah |
|---|---|---|
| Secret | `GATEWAY_SIGNING_KEY_PEM` | Privátní PKCS8 podpisový klíč |
| Secret | `GATEWAY_PUBLISHER_TOKEN` | Samostatný token pouze pro publikování vydání |
| Variable | `GATEWAY_SIGNING_KEY_ID` | Např. `release-1`, shodný s provisionovanými anchors |
| Variable | `GATEWAY_RELEASE_ORIGIN` | Např. `https://cloud.stitkovac.app`, bez koncového lomítka |

Podpisový klíč je v dočasném privátním souboru jen při podpisu a po kroku se
odstraní. Do CI artefaktů patří pouze agent, podepsaný manifest a veřejné klíče.
Publisher skript nepovoluje redirecty a nezobrazuje serverová těla odpovědí ani
credentials. Úspěšný publish neplánuje rollout. Opakované vydání stejné verze
je odmítnuté; oprava programu musí mít nové číslo verze.

Veřejné klíče se na server a brány distribuují odděleně od artefaktů.
Při rotaci nejprve servisně přidat nový veřejný klíč při zachování starého,
potom publikovat nově podepsané vydání. Starý klíč odstranit až po ověření,
že nepodepisuje aktivní, předchozí ani pending vydání žádné dotčené brány.
Server dostává pouze veřejnou mapu, nikdy privátní podpisový klíč.

## Provisioning a servis

Na připraveném ARM64 Linuxu nejprve provést `bootstrap.yml` a podle zařízení
`network.yml`; potom offline `deploy/ansible/ota.yml`. Root je při instalaci
zapisovatelný, `/data` musí být samostatné ext4, agent musí být zastavený.
Playbook neformátuje disk, nemění identitu ani cloudový token a nespouští službu
nad dosud zapisovatelným systémem.

V soukromém inventory nastavit `gateway_ota_provision: true`,
`gateway_release_version`, cesty `gateway_release_manifest`,
`gateway_release_artifact`, `gateway_release_keys` a HTTPS origin
`gateway_release_repository`. Žádný privátní podpisový klíč se nekopíruje.

```sh
ansible-playbook -i /private/gateways/inventory.yml deploy/ansible/ota.yml
```

Playbook instaluje stabilní launcher/updater, vytvoří privátní release adresář,
ověří podpis a prvotní selection a přidá `zz-ota.conf`. Zachová síťové argumenty
z aktuálního `network.yml` (`GATEWAY_NETWORK_ARGS` pro servisní AP).
Starší standardní síťový drop-in automaticky převede se zachováním adresy AP;
produkční síťový provisioning se nepouští na VirtualBox NAT laboratoři.
Při opakování nesmí nové trust anchors zneplatnit aktivní/předchozí/pending vydání.
Nakonec obnovit read-only root a boot a teprve pak spustit službu. V administraci
ověřit novou verzi a OTA připravenost. Samostatně spuštěný agent OTA nehlásí.

`gateway-update` poskytuje `download`, `stage`, `initialize`, `request`,
`rollback`, `verify` a `status`. Volby `--releases`, `--keys`, `--version`,
`--manifest`, `--artifact`, `--repository` jsou explicitní. Ruční `request`
a `rollback` jsou servisní nástroje; samy nezastavují tisk. Při běžném provozu
používat serverový rollout s dokončením tisku a aktivačním grantem.

## Ověření a zbývající provozní kroky

[Záznam provedených kontrol](ota-verification.md) odděluje softwarové testy
od dosud neprovedené produkční aktivace a hardwarového pilotu.

- Go race testy: podpisy, checksumy, protokoly, staging, retence, řízený rollback,
  monotónní durable state, grant, odmítnutí změny registrace při aktivaci,
  čekání na dokončení workeru, obnova po trial a opakování ztraceného reportu.
- Procesní testy launcheru: readiness, timeout, pád, nesprávná verze,
  restart kódem 75, potvrzení před povolením cloudu.
- `scripts/test-ota.sh`: skutečný podepsaný ARM64 agent v izolovaném kontejneru,
  neúspěšný trial a obnovení původní verze. Jednorázové testovací klíče se mažou.
- Serverové `GatewayOtaTests`: hosting, chunking, podpis, role a scoped publisher,
  kolize nasazení, pause/cancel/expiry, fencing aktivace, audit a oddělení bran.
- Cross-repository `TestSpringOTA`: skutečný Go transport a OTA controller proti
  Spring/MariaDB, WSS příkaz, HTTPS artefakt, blokovaný TCP přenos, dokončení,
  trvalý výběr nové verze a rekonciliace původního tisku bez opakování bajtů.
  Boot v tomto testu potvrzuje testovací harness; skutečný handshake je pokrytý
  oddělenými procesními a ARM64 testy.

K zákaznickému nasazení stále zbývá produkční konfigurace klíčů/CI/serveru,
provisioning konkrétní brány a pilot na reálné SD kartě s odebráním napájení.
Testy atomického stavu a procesu nepotvrzují fyzickou odolnost konkrétního média.
