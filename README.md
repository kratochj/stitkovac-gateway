# Štítkovač Gateway

Tisková brána pro provozovny, kde se klientská zařízení a síťové tiskárny nemohou
spojit přímo. Brána přijímá tiskové úlohy ze Štítkovače přes internet a předává
jejich dokumenty tiskárnám připojeným ethernetem.

Projekt je samostatnou součástí workspace vedle `stitkovac-server`,
`stitkovac-app` a ostatních projektů. Obsahuje implementaci agenta;
není zatím připravený pro produkční instalaci u zákazníka.

**[Specifikace řešení](docs/specifikace.md)** pokrývá architekturu, lokální web,
správu bran v serveru, tiskový protokol, instalaci pomocí Ansible a ověřovací scénáře.

Výchozí návrh: Raspberry Pi OS Lite, Ansible, agent jako služba `systemd`,
odchozí WSS spojení a privátní ethernetová síť tiskáren.
Tiskárny získávají IP přes DHCP; první lease se automaticky uloží jako trvalá
rezervace podle MAC adresy.
Brána počítá s běžným vypínáním odpojením napájení: read-only systém, oddělený
trvalý datový oddíl a obnova rozpracovaného tisku bez automatických duplicit.
Chyby se hlásí do Rollbaru přes server Štítkovače.

## Implementováno

- Explicitní inicializace identity, soukromého úložiště a HTTPS certifikátu.
- SQLite WAL/FULL journal a obnova nejistých tiskových pokusů bez automatického opakování.
- DHCPv4 s trvalou MAC/IP rezervací před OFFER a záznamem lease před ACK.
- HTTPS servisní přihlášení s Argon2id a přehled nalezených zařízení.
- Odchozí WSS, obnovování spojení, HTTPS claim/download/start/result a RAW TCP tisk.
- Omezená trvalá fronta očištěných chyb pro serverový Rollbar relay.
- ARM64 build, systemd služba, Ansible bootstrap/diagnostika a sestavení `.deb` na Linuxu.

## Vývoj

Potřebujete Go podle `go.mod`. Síťové integrační testy otevírají pouze lokální
TLS server. Testy nekomunikují se skutečnou tiskárnou, produkčním serverem ani Rollbarem.

```sh
make check test build arm64
```

Lokální spuštění: vytvořte soukromý soubor s heslem o alespoň 16 znacích.
Heslo se nepředává argumentem příkazu a nepatří do repozitáře.

```sh
chmod 600 /path/to/private-password
bin/gateway init --data-dir .local/state --password-file /path/to/private-password
bin/gateway serve --data-dir .local/state
```

Web je na `https://127.0.0.1:8443`. Při první návštěvě ověřte fingerprint
certifikátu vypsaný při inicializaci. DHCP a tisk přes svázané rozhraní vyžadují
Linux; lokální příkaz bez dalších parametrů nespouští DHCP ani cloudové spojení.

Další informace:

- [Stav implementace a zbývající práce](docs/implementation-status.md).
- [Instalace a bezpečný pilot](docs/installation.md).
- [Gateway API v1](docs/protocol-v1.md).
- [Rozhodnutí o DHCP backendu](docs/adr-001-dhcp.md).
