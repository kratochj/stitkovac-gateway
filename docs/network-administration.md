# Síťová administrace

Implementace od agenta **0.1.4**. V lokálním webu otevřete **Síť** (`/network`).
Software je implementovaný a ověřený v testech a ARM64 Linuxu; rádiovou část,
GPIO a skutečné odebrání napájení ještě musí ověřit pilot na konkrétním Raspberry Pi.

## Ovládání

- Stav Wi-Fi, IPv4, výchozí brány, DNS a oddělené tiskové sítě.
- Vyhledání SSID, ruční zadání včetně skryté sítě, země provozu, WPA2 Personal,
  WPA3 Personal nebo otevřená síť. Heslo se při změně zadává znovu.
- Uložení spustí pokus o připojení. Teprve po ověření asociace, IP, DNS a TLS
  serveru se nový profil stane trvalou volbou. Token serveru tento test nepoužívá.
- Pokus má limit 55 sekund včetně krátkého odkladu pro odeslání webové odpovědi.
  Obnova má samostatný limit 30 sekund. Vrací se předchozí profil; pokud jeho
  aktivace selže, zbývající čas se použije pro servisní AP. Nelze garantovat
  zprovoznění vadného nebo odpojeného adaptéru; takový stav web zobrazí.
- Servisní AP lze zapnout z webu dostupného po ethernetu nebo pětisekundovým
  podržením servisního tlačítka. Jedno držení vyvolá jednu akci, další vyžaduje
  puštění tlačítka. Během změny sítě se další požadavek odmítne jako obsazený.
- AP s uloženou Wi-Fi má pevný limit 15 minut od aktivace, poté se vrací uplink.
  Bez přijatého profilu AP zůstává zapnuté. Běžný výpadek Wi-Fi/internetu se
  řeší opakováním uloženého profilu, nikoli automatickým zapínáním AP.

Některé adaptéry neumějí vyhledávat sítě, když vysílají AP. V takovém případě
web nabídne ruční zadání SSID; kvůli samotnému vyhledání nepřeruší servisní přístup.
802.1X a captive portal nejsou podporované. Rozsahy tiskové sítě a AP se nastavují
v inventory. Změna živých DHCP rezervací a serverových endpointů není součástí
Wi-Fi formuláře. Překryv uplinku s některým soukromým subnetem zablokuje přijetí profilu.

## Oddělení oprávnění a persistence

Web běží pod `stitkovac-gateway`. Root proces `gateway-network` přijímá pouze
stav, scan, Wi-Fi konfiguraci a přepnutí AP/uplink přes Unix socket
`/run/stitkovac-gateway-network/control.sock`. Socket má režim 0660 a root adresář
0750. Linux `SO_PEERCRED` navíc povoluje pouze UID agenta nebo roota, nikoli
libovolného člena skupiny. HTTP má časové a velikostní limity a odmítá neznámá pole.
Web zachovává přihlášení, CSRF a shodu Origin s konkrétním povoleným Hostem.

Helper spouští pevně určené operace `nmcli`, `iw`, `nft` a `systemctl`, bez shellu.
Hesla nejsou v argumentech procesů, odpovědích, surových chybách ani logu.
NetworkManager dostává kořenové keyfiles s režimem 0600. SSID je zapsané jako
pole bajtů, takže ani středníky či zpětná lomítka nemění význam profilu.
TLS test používá sockety vázané na Wi-Fi, kontroluje certifikát a nesleduje přesměrování.
Samotné spojení jinou kartou tedy nemůže potvrdit nefunkční Wi-Fi.

| Cesta | Obsah |
|---|---|
| `/data/network/selection.json` | Přijatý profil, případný pokus, režim AP a jeho termín |
| `/data/network/profiles` | Privátní NetworkManager keyfiles |
| `/data/network/nm-state` | Trvalý stav NetworkManageru |
| `/data/network/ap-credentials.json` | SSID odvozené z Wi-Fi MAC, náhodné heslo a UUID servisního AP |
| `/etc/stitkovac-gateway/network.json` | Root konfigurace helperu, rozhraní, rozsahy a GPIO |

Profily a journal se zapisují atomicky s fsync souboru i adresáře. Po restartu
se rozpracovaný pokus vždy zahodí a obnoví předchozí volba. Wi-Fi profily mají
`autoconnect=false`, aby NetworkManager sám nepřipojil neověřený pokus při bootu.
Reconnect přijatého profilu řídí helper. Opuštěné profily uklízí až po obnově;
profil aktuálního pokusu se nesmí smazat souběžným úklidem.
Chyba zápisu zastaví další změny konfigurace místo přijetí neuloženého nastavení.

Helper patří do připravovaného OS image; současné aplikační OTA jej nepřepisuje.
Jeho upgrade vyžaduje zastavenou službu a servisní provisioning.

NetworkManager profily a stav se bind-mountují z `/data` před startem služby.
DNS je v `/run/NetworkManager/resolv.conf`; AP DHCP lease soubor je pouze v `/run`.
Tiskové MAC rezervace zůstávají ve stávající databázi agenta a tento celek je nemění.
Síťové chyby jsou v lokálním stavu a journalu služby. Nové síťové kódy pro serverový
Rollbar relay zatím nejsou součástí jeho allowlistu; existující diagnostika tisku
ani její předávání serveru se nemění.

## Firewall a přístup

Žádný bridge, NAT ani forwarding. Samostatná nftables tabulka má výchozí drop
na input i forward. Povoluje odpovědi navázaných spojení, DHCP uplinku,
DHCP tiskáren a servisní přístup z oddělených sítí. Web poslouchá jen na výslovných
IP adresách tiskového ethernetu a AP. `IP_FREEBIND` dovolí připravit posluchače
ještě před aktivací rádia. SSH navíc omezuje firewall na tiskovou a servisní síť.

Přístup z Wi-Fi na AP DHCP, SSH a HTTPS řídí dynamická nftables množina:
helper ji otevře až po skutečné aktivaci AP a uzavře před přechodem na uplink.
Pouhé rozpoznání IP subnetu by nestačilo – zákazníkova Wi-Fi může během neúspěšného
pokusu přidělit kolidující adresu. Po ručním reloadu firewallu restartujte helper;
reload přístup do AP preventivně uzavře.
AP používá vlastní DHCP-only dnsmasq, bez DNS, výchozí trasy a trvalých rezervací.
Při přechodu na uplink se služba DHCP zastaví. Tiskový DHCP agenta zůstává nezávislý.

## Instalace na Raspberry Pi

Nejdříve připravte image s odděleným `/data` a proveďte
[bootstrap agenta](installation.md). Síťovou část spouštějte při přípravě zařízení,
s writable rootem a zastaveným agentem, z konzole nebo nezávislé servisní cesty.
Playbook síť zapíná až při následujícím bootu a nenahrazuje přípravu diskových oddílů.

```sh
make VERSION=0.1.4 arm64
ansible-playbook -i /path/to/private-inventory.yml deploy/ansible/network.yml \
  -e gateway_network_provision=true
```

Používejte stejné tiskové IP, prefix a pool jako v bootstrapu. Výchozí hodnoty:

| Proměnná | Hodnota |
|---|---|
| `gateway_wifi_interface` | `wlan0` |
| `gateway_printer_interface` | `eth0` |
| `gateway_printer_address` / `gateway_printer_prefix` | `192.168.77.1` / `24` |
| `gateway_ap_address` / `gateway_ap_network` | `192.168.78.1` / `192.168.78.0/24` |
| `gateway_ap_first` / `gateway_ap_last` | `192.168.78.50` / `192.168.78.199` |
| `gateway_country` | `CZ` |
| `gateway_gpio_chip` / `gateway_gpio_line` | vypnuto / `17` |

Playbook nainstaluje NetworkManager, iw, dnsmasq-base a nftables, persistence mounty,
statický ethernetový profil bez default route, helper a jeho služby, AP DHCP,
firewall a vypnutí forwardingu. Zakáže konfliktní legacy network managery a diskový
swap; následný boot je nutný. Existující profily ze základního image se nemažou,
ale překryje je nový bind mount. První přijatou zákaznickou Wi-Fi nastavte přes web.

AP heslo vzniká na zařízení a nikdy se nevypisuje do Ansible výstupu. Technik je
z root-only souboru převede do správce hesel a označí zařízení. Není shodné s heslem
webu. Opakovaný provisioning AP přístupy ani přijatý Wi-Fi profil nepřegeneruje.
Certifikát webu se rozšíří o AP IP při zachování privátního klíče. Ve výstupu je nový
veřejný fingerprint; po rozšíření je potřeba znovu ověřit důvěru certifikátu.

Zapněte agenta, obnovte read-only root i boot a restartujte. Před provozem musí
projít `check-storage`. Bez platného přijatého profilu očekávejte servisní AP a
web na `https://192.168.78.1:8443`. Při změně rozsahů použijte skutečnou AP IP.
NetworkManager ani skutečný firewall z tohoto playbooku neinstalujte do VirtualBox labu.

### GPIO

Používá se Linux GPIO character device API v2, vstup active-low s pull-up.
Do inventory nastavte skutečný `/dev/gpiochipN` a offset linky pro daný model.
Tlačítko propojí zvolený GPIO se zemí. Na Pi 3 B+ odpovídá BCM17 fyzickému pinu 11;
číslo gpiochipu a dostupnost linky přesto ověřte v konkrétním image. Vstup nikdy
nepřipojujte na 5 V. Bez nastaveného `gateway_gpio_chip` se GPIO neotevírá.
Neúspěšné otevření linky se zaznamená do systemd journalu a UI tlačítko neprezentuje
jako dostupnou cestu. Skutečné zapojení musí projít testem na hardware.

## Ověření a VirtualBox

Ověřeno:

- `go vet`, formátování, kompletní `go test -race ./...`, macOS a Linux ARM64 build.
- Testy rollbacku, timeoutu, restartu s rozpracovaným pokusem, AP fallbacku,
  automatického návratu, běžného reconnectu, parsování a dlouhého stisku.
- Skutečný ARM64 helper v Debianu: kontrola UID přes Unix socket, násilné ukončení
  a obnova, načtení vygenerovaných klientských i AP profilů skutečnou knihovnou libnm
  včetně přesného round-tripu SSID a hesla s problematickými znaky.
- Validace dnsmasq a nftables, opakovaná aplikace pravidel a uzavření AP přístupu.
  Skutečný DHCP-only dnsmasq běží v integračním testu pod neprivilegovaným UID
  s produkčními capabilities na izolovaném dummy rozhraní.
- Browserový test proti běžící VirtualBox VM: scan, přijetí Wi-Fi, odmítnutí pokusu,
  návrat na profil, AP/uplink, přihlášení, CSRF/Origin, mobilní layout a zachované
  připojení k dosavadnímu serveru. Test používá pouze simulovaný backend.

```sh
make VERSION=0.1.4 check test build arm64
sh scripts/test-network.sh
ansible-playbook -i deploy/virtualbox/inventory.yml deploy/virtualbox/network.yml
GATEWAY_TEST_CHROMIUM=/path/to/chromium node scripts/virtualbox/smoke-browser.mjs --network
```

Ve VM je backend výrazně označený jako **Simulace sítě**. SSID `Simulated failure`
vyvolá odmítnutí pokusu; ostatní platné údaje uspějí. Testy nikdy nekonfigurují NAT
ani skutečné rozhraní VM. Aktuální VM obsahuje vlastní serverový token – její OVA
se kvůli této změně neexportuje. Existující OVA 0.1.2 zůstává starší samostatný lab.

Před zákaznickou instalací zbývá ověřit skutečný Wi-Fi driver (scan/AP/WPA3),
GPIO, fyzickou izolaci sítí, DHCP interoperabilitu a napájení na vybraném RPi.
Rozdělení SD a read-only nastavení konkrétního image zůstává samostatnou přípravou OS.

## Technické zdroje

- [NetworkManager keyfiles](https://networkmanager.pages.freedesktop.org/NetworkManager/NetworkManager/nm-settings-keyfile.html)
- [nmcli a jeho offline režim](https://networkmanager.pages.freedesktop.org/NetworkManager/NetworkManager/nmcli.html)
- [Linux GPIO character device API](https://docs.kernel.org/userspace-api/gpio/chardev.html)
- [GPIO v2 UAPI definice](https://raw.githubusercontent.com/torvalds/linux/master/include/uapi/linux/gpio.h)

Hostname a SSID nových appliance image používají shodný formát podle Wi-Fi MAC;
[pravidla a servisní přejmenování](device-naming.md).
