# Testovací brána ve VirtualBoxu

Připraveno a ověřeno 2026-09-29 na MacBooku s Apple Silicon, macOS 26.4.1
a VirtualBoxem 7.2.4. VM používá Debian 13 ARM64, 2 CPU, 2 GB RAM,
8GB systémový a 2GB datový disk. Tento obraz není pro Intel Mac.

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
Produkční účet, token ani Rollbar nejsou v této VM nastavené. Diagnostické
události přijímá pouze lokální simulátor.

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

Simulátor uchovává nejvýše 100 úloh a posledních 50 zachycených dokumentů.
`SENT` znamená úspěšné předání bajtů TCP simulátoru, ne důkaz fyzického tisku.
Tento test neověřuje Wi-Fi/AP, kompatibilitu Honeywell PC42E, SD kartu,
skutečné odpojení napájení Raspberry Pi ani výpadek uprostřed fyzického tisku.
Síťová administrace a kompletní serverové OTA jsou stále rozpracované.

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
