# Štítkovač Gateway

Tisková brána pro provozovny, kde se klientská zařízení a síťové tiskárny nemohou
spojit přímo. Brána přijímá tiskové úlohy ze Štítkovače přes internet a předává
jejich dokumenty tiskárnám připojeným ethernetem.

Projekt je samostatnou součástí workspace vedle `stitkovac-server`,
`stitkovac-app` a ostatních projektů. Obsahuje zatím pouze návrh řešení.

**[Specifikace řešení](docs/specifikace.md)** pokrývá architekturu, lokální web,
správu bran v serveru, tiskový protokol, instalaci pomocí Ansible a ověřovací scénáře.

Výchozí návrh: Raspberry Pi OS Lite, Ansible, agent jako služba `systemd`,
odchozí WSS spojení a privátní ethernetová síť tiskáren.
Tiskárny získávají IP přes DHCP; první lease se automaticky uloží jako trvalá
rezervace podle MAC adresy.
Brána počítá s běžným vypínáním odpojením napájení: read-only systém, oddělený
trvalý datový oddíl a obnova rozpracovaného tisku bez automatických duplicit.
Chyby se hlásí do Rollbaru přes server Štítkovače.
