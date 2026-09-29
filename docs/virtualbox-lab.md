# Testovací brána ve VirtualBoxu

Připraveno a ověřeno 2026-09-29 na MacBooku s Apple Silicon, macOS 26.4.1
a VirtualBoxem 7.2.4. VM používá Debian 13 ARM64, 2 CPU, 2 GB RAM,
8GB systémový a 2GB datový disk. Tento obraz není pro Intel Mac.

Aktuálně registrovaná VM používá podepsaného agenta **0.1.5** přes OTA launcher se [síťovou administrací
a její simulací](network-administration.md) a [diagnostikou tisku](print-diagnostics.md). Existující OVA z předchozího exportu obsahuje
**0.1.2**. Nový export z aktuální VM nebyl vytvořen; zachovává se její nastavené
připojení k serveru. Sestavení nové čisté laboratoře ze zdrojů používá 0.1.4.

## Spuštění připraveného buildu

VM **Stitkovac Gateway Lab** je na tomto Macu už zaregistrovaná.

1. Otevři `dist/virtualbox/PRISTUPY.txt`, kde je vygenerované heslo.
2. Spusť dvojklikem `dist/virtualbox/Spustit.command`. Skript zapne VM,
   vytvoří lokální SSH tunely a otevře laboratoř v prohlížeči.
3. Na `https://127.0.0.1:9443/` se přihlas jako **technik** uvedeným heslem.
4. Počkej na WSS připojení brány a tiskárnu `192.168.77.50:9100`.
5. Klikni na testovací štítek nebo účtenku. U dokončené úlohy uvidíš `SENT`
   a přijaté PDF, které lze otevřít nebo stáhnout.
6. Administrace skutečného agenta je na `https://127.0.0.1:8443/`.
   Používá stejné heslo. Zobrazuje také nalezenou tiskárnu a rezervaci DHCP.

Oba HTTPS servery mají vlastní certifikát, takže prohlížeč při první návštěvě
zobrazí varování. Ve složce buildu jsou jejich veřejné kopie. Fingerprint ověříš:

```sh
openssl x509 -in dist/virtualbox/lab-cert.pem -noout -fingerprint -sha256
openssl x509 -in dist/virtualbox/gateway-cert.pem -noout -fingerprint -sha256
```

VM vypni přes `dist/virtualbox/Vypnout.command`. Po zavření prohlížeče běží dál.
Startovací skripty potřebují Python 3 a VirtualBox dostupný přes `VBoxManage`.
Z Terminálu lze použít i `python3 scripts/virtualbox/start.py --open`.

## Co se skutečně testuje

Build **0.1.2** přidává do administrace **Připojení k serveru**. Lze nastavit
produkční adresu `https://cloud.stitkovac.app` a token vydaný na serveru pro ID
této brány. Postup je v [instalaci](installation.md#připojení-k-serveru).
Tím brána přejde z lokálního simulátoru na skutečný server; simulovaná tiskárna
zůstává připojená na stejné MAC/IP. Tlačítka laboratoře potom neposílají úlohy
do této brány. Pro návrat nastavte `https://127.0.0.1:9443` a token z položky
`gateway_lab_token` v soukromém `dist/virtualbox/credentials.json`.
Uložené nastavení přetrvá restart a má přednost před parametry z instalace.

```text
Prohlížeč → SSH tunel → lokální HTTPS/WSS simulátor serveru
                                    ↓ oznámení nové úlohy
                              skutečný gateway agent
                                    ↓ PDF přes TCP :9100
                      simulovaná tiskárna v oddělené síti
```

Tiskárna má vlastní Linux network namespace a MAC `02:77:00:00:00:01`.
BusyBox DHCP klient získává adresu od skutečného DHCP serveru agenta.
První přidělení agent trvale rezervuje podle MAC. TCP simulátor ukládá přesné
přijaté bajty do `/data/lab/prints`; netiskne na fyzickém zařízení.

Stejný ARM64 agent jako pro Raspberry Pi používá skutečný WSS transport,
claim/download/start/result protokol, journal a TCP socket. Lokální pomocný
server nahrazuje produkční Štítkovač a generuje dvě malá testovací PDF.
Čistý export OVA nemá produkční účet ani token. Aktuálně registrovaná VM už
má vlastní připojení k produkčnímu serveru; jeho token se při aktualizaci
zachovává a tato VM se nesmí použít jako distribuční export.

Základní systém a EFI jsou připojené read-only. `/data` je samostatné ext4,
dočasné soubory a systémové logy jsou v RAM. Po vypnutí tedy systémové logy
zmizí; evidence úloh, rezervace a přijatá PDF zůstávají.

VM má jediný NAT uplink. Na hostiteli se předává jen SSH na `127.0.0.1:2222`;
weby se zpřístupňují přes SSH na loopbacku. DHCP tiskárny neopouští izolovanou
síť VM. Uplink používá DNS `1.1.1.1`, protože NAT DNS proxy VirtualBoxu při
ověření na tomto Macu neodpovídala. Samotný tiskový test internet nepotřebuje.

## Ověření tohoto buildu

- Kompletní `make check test` včetně race detectoru prošlo.
- Ve skutečné VM prošel štítek i účtenka cestou WSS → agent → TCP simulátor,
  obě úlohy skončily `SENT`; kontroloval se obsah i SHA-256 zachycených PDF.
- Po tvrdém vypnutí přes VirtualBox Power Off a opětovném startu zůstala
  rezervace `.50` i obě PDF. Nevznikl opakovaný tisk dokončených úloh.
- Po následném restartu prošel produkční storage guard, všechny služby
  laboratoře běžely a `systemctl --failed` byl prázdný.
- Export OVA prošel kontrolním `VBoxManage import --dry-run`, který rozpoznal
  Debian ARM64, oba disky, NAT a VirtioSCSI. Druhá importovaná VM se nespouštěla.

Metadata tiskového testu jsou v `dist/virtualbox/smoke-results.json`.
Další test lze spustit `python3 scripts/virtualbox/smoke.py`, když VM běží
a jsou otevřené tunely. Přidá další dvě úlohy.

Build **0.1.1** opravuje přihlášení do administrace: původní `Referrer-Policy:
no-referrer` měnila Origin formuláře na `null`, který server správně odmítal.
Politika `same-origin` zachovává původ vlastního formuláře a neposílá referrer
jiným webům. Kontrola Origin ani CSRF nebyla vypnutá. Chyba byla zopakována
a oprava ověřena skutečným Chromium včetně dashboardu a odhlášení.
Chování hlavičky popisuje [Fetch Standard](https://fetch.spec.whatwg.org/#append-a-request-origin-header).
Po aktualizaci znovu načti `/login`; neodesílej starý formulář z historie.

Pro regresní test prohlížeče nainstaluj izolovanou testovací závislost a nastav
cestu k existujícímu Chromium. Test načítá heslo ze soukromého souboru buildu,
nevypisuje ho a nemění systémové úložiště certifikátů:

```sh
npm install --prefix .cache/browser-tests --no-audit --no-fund --ignore-scripts playwright-core@1.58.2
GATEWAY_TEST_CHROMIUM=/absolute/path/to/chromium node scripts/virtualbox/smoke-browser.mjs
```

Volba `--configure-lab` navíc ověří odmítnutí neplatného tokenu, uložení správného
tokenu, obnovení WSS spojení a ponechání tokenu při prázdném poli. Použijte ji
pouze proti lokálnímu simulátoru; test odmítne přepsat konfiguraci jiného serveru.

Volba `--diagnostics` ověřuje historii, filtr, mobilní zobrazení a TCP spojení
se simulovanou tiskárnou. Cloudovou konfiguraci nemění a neodesílá tisková data.

Simulátor uchovává nejvýše 100 úloh a posledních 50 zachycených dokumentů.
`SENT` znamená úspěšné předání bajtů TCP simulátoru, ne důkaz fyzického tisku.
Tento test neověřuje Wi-Fi/AP, kompatibilitu Honeywell PC42E, SD kartu,
skutečné odpojení napájení Raspberry Pi ani výpadek uprostřed fyzického tisku.
Síťová administrace používá simulaci rádia. OTA launcher je ve VM nasazený;
serverová OTA část je nasazená ve verzi 2.7.13.

## Export a opětovný import

Export `dist/virtualbox/stitkovac-gateway-lab-arm64.ova` obsahuje oba disky;
soubor `.ova.sha256` obsahuje kontrolní součet. Součástí obrazu jsou i dva
ověřovací tisky a vygenerované testovací přístupy. Nejde o anonymní distribuční
image. Celou složku `dist/virtualbox` včetně SSH klíče uchovávej soukromě.

Na tomto Macu není import potřeba. Pro obnovu použij ve VirtualBoxu Import
Appliance a ponech název **Stitkovac Gateway Lab**. Stejnojmenná původní VM
nesmí být současně registrovaná. Importovaná kopie potřebuje zachovat NAT SSH
forward `127.0.0.1:2222 → 22`; dvě kopie nemohou používat stejné hostitelské porty.
Spouštěcí skripty dále potřebují tento checkout a původní `dist/virtualbox`
s `lab.json`, soukromým SSH klíčem a přístupy. OVA sama neobsahuje hostitelský
soukromý SSH klíč. Do VM je přihlášení heslem přes SSH vypnuté.

## Sestavení nového obrazu

Vyžaduje Apple Silicon, VirtualBox 7.2, Python 3.11+, Go dle `go.mod`,
Ansible, `qemu-img`, `curl` a macOS `hdiutil`. Sestavení spouštěj z tohoto
repozitáře, když ještě neexistuje `dist/virtualbox/lab.json` ani stejnojmenná VM.
Skripty existující bránu nepřepisují a nejsou upgradem běžící laboratoře.

```sh
sh scripts/virtualbox/build.sh
```

Skript stáhne připnutý Debian image, ověří jeho SHA-512, sestaví binárky,
vytvoří VM a privátní SSH klíč, provede Ansible provisioning, restartuje do
read-only režimu, vytiskne dvě PDF a po čistém vypnutí vyexportuje OVA.
Cloud-init při prvním bootu instaluje systémové balíčky z Debian repozitářů;
jejich verze nejsou zamčené. Výsledný obraz proto není bitově reprodukovatelný.
Při přerušeném buildu stav nejprve prohlédni; skript automaticky nemaže VM ani disky.

Použitý základ: [Debian 13 generic ARM64, 20260914-2601](https://cloud.debian.org/images/cloud/trixie/20260914-2601/).
Kontrolní součet SHA-512 je připnutý v `scripts/virtualbox/create.py`.
Virtuální hardware se připravuje tímto skriptem; konfigurace Linuxu je v
`deploy/virtualbox/provision.yml`, která znovu používá běžný Ansible bootstrap.
Konkrétní dodaný obraz byl sestaven těmito kroky průběžně; závěrečný wrapper
má syntaktickou kontrolu, druhé kompletní sestavení od nuly neproběhlo.

## Síťová administrace 0.1.4

Síťová administrace byla nasazená ve verzi 0.1.4. Stránka **Síť** obsahuje explicitně
označenou simulaci Wi-Fi a AP. Skutečný NAT, tiskárna a cloudové přístupy zůstávají
zachované. Detaily a opakovatelný instalační/testovací postup jsou v
[síťové administraci](network-administration.md). Existující export OVA 0.1.2
se nemění; aktualizovaná VM s vlastním cloudovým tokenem se neexportovala.


## OTA launcher 0.1.5

Dne 2026-09-29 byl na existující VM nasazen podepsaný agent **0.1.5**, launcher
protokolu 2 a updater. Aplikace běží z `/data/gateway-releases/0.1.5/gateway`.
Původní přímá binárka na systémovém oddílu není aktivní. Identita, heslo,
cloudový token, tiskový journal, DHCP rezervace a síťová simulace zůstaly zachované.

Veřejný testovací klíč `virtualbox-lab-20260929` je v systémovém souboru
`/etc/stitkovac-gateway/release-keys.json`. Soukromý klíč zůstává výhradně
na Macu v ignorovaném `dist/virtualbox/ota/lab-signing.pem`; není to produkční
podpisový klíč. Podepsané vydání, veřejné klíče a instalační parametry jsou
ve stejné privátní složce. Existující OVA 0.1.2 se nemění.

Opakování provisioningu stejného vydání, z kořene projektu:

```sh
ANSIBLE_LOCAL_TEMP=/tmp/stitkovac-ansible ANSIBLE_PIPELINING=True \
  ansible-playbook -i deploy/virtualbox/inventory.yml deploy/virtualbox/ota.yml \
  -e @dist/virtualbox/ota/provision.json
```

Playbook nejprve ověří laboratoř a klidný tisk, zastaví agenta, dočasně přepne
root do zápisu a použije společné OTA úlohy. V bloku `always` obnoví read-only
root a službu. Kontroluje nezměněný checksum cloudové konfigurace bez výpisu
obsahu, HTTPS startup a potvrzený výběr verze. Opakovaný provisioning nemění
aktivní výběr existující instalace; pro další vydání použijte OTA příkaz.

Ověřeno na skutečné VM:

- Podepsaný start 0.1.5 přes launcher a opakovaný Ansible provisioning.
- Restart celé VM: všechny služby aktivní, žádné failed units, potvrzená verze
  0.1.5, storage guard a následná webová diagnostika znovu prošly.
- Přihlášení, připravenost OTA, připojení k původnímu serveru, TCP diagnostika
  simulované tiskárny, historie a mobilní zobrazení (`smoke-browser.mjs --ota --diagnostics`).
- Automatický návrat po záměrně chybné verzi v podepsaném manifestu.
  `smoke-ota-guest.sh` používá samostatný network namespace a dočasný privátní
  adresář na `/data`; nemění běžící službu, její databázi ani aktivní vydání.
  Testovací manifest 9.9.9 není skutečné vydání a nesmí se publikovat.

Repository origin je `https://cloud.stitkovac.app`. Server **2.7.13** má nové
OTA endpointy i testovací veřejný klíč; VM se po rollout znovu připojila a její
autentizované OTA API vrátilo HTTP 200 s prázdnou frontou. Podepsaný balíček
0.1.5 je publikovaný a veřejný manifest odpovídá lokálnímu podepsanému vydání.
Skutečný updater ve VM jej stáhl z produkčního HTTPS do odděleného adresáře
a ověřil podpis, SHA-256 i verzi binárky; aktivní instalace se nezměnila.
Lokální simulátor na portu 9443 OTA rollout neimplementuje. Dosavadní ověření
není nový end-to-end test vzdálené aktualizace běžící brány.

## Živý přehled tisku v laboratoři

Web laboratoře na portu 9443 rozlišuje dva zdroje dat:

- **Dokumenty přijaté tiskárnou** jsou skutečné TCP přenosy uložené simulátorem,
  také při tisku z cloudového serveru. Přehled ukazuje čas přijetí, velikost
  a odkaz na původní dokument. Nezná ID ani konečný stav cloudové úlohy;
  ty jsou v administraci serveru a v historii samotné brány na portu 8443.
- **Historie úloh místního testovacího serveru** obsahuje jen tisky vytvořené
  tlačítky laboratoře. Po přepojení brány na cloud zůstává její starší obsah
  zachovaný. `SENT` znamená předané bajty, nikoli potvrzený fyzický tisk.

Přehled se obnovuje přes autentizovaný SSE stream `/events`, bez reloadu stránky.
Server každou sekundu kontroluje lokální soubory zachycené samostatným procesem
simulátoru; nepoluje cloudovou tiskovou frontu. Změny odesílá do prohlížeče
hned při dalším snímku, heartbeat každých 10 sekund. Web ukazuje poslední ověření
stavu. Při chybě spojení, přechodu offline nebo chybějícím heartbeat označí data
jako neaktuální a po obnovení spojení načte celý současný stav.

Místní testovací tlačítka jsou aktivní pouze s připojenou bránou a DHCP adresou;
server odmítne také ručně odeslaný formulář, pokud místní WSS spojení chybí.
Staré úlohy bez časových údajů ukazují „Čas nebyl zaznamenán“. Nové úlohy mají
trvale uložený čas vytvoření a poslední změny stavu.

Tato oprava patří do **gateway-lab**, nikoli do OTA balíčku agenta 0.1.6.
Je připravená ve zdrojích a ARM64 binárce `bin/gateway-lab-linux-arm64`;
běžící VM ani server nebyly při této opravě aktualizované.

Regrese: `go test -race ./internal/lab`, Go vet a ARM64 build pomocného procesu.
Volitelný prohlížečový test používá oddělený dočasný HTTPS server a TCP tiskárnu
na Macu. Nečte přístupy ani konfiguraci VM:

```sh
mkdir -p .cache/lab-live-browser
rm -f .cache/lab-live-browser/done .cache/lab-live-browser/fixture.json
GATEWAY_LAB_BROWSER_DIR="$PWD/.cache/lab-live-browser" \
  go test -run '^TestLabBrowserFixture$' -count=1 -v ./internal/lab
```

Po vzniku `fixture.json` spusť v druhém terminálu s nastaveným
`GATEWAY_TEST_CHROMIUM` (stejně jako u ostatních smoke testů):

```sh
GATEWAY_LAB_BROWSER_DIR="$PWD/.cache/lab-live-browser" \
  node scripts/virtualbox/smoke-lab-live.mjs
```

Test ověřuje skutečný TCP příjem a automatické zobrazení bez navigace, heartbeat
přes běžný HTTP write timeout, ztrátu a obnovu spojení, zachování oddělené historie
a mobilní šířku 390 px. Po dokončení se testovací servery ukončí.
