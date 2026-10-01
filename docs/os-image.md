# Kompletní appliance OS image

`scripts/image/build.py` připravuje obecný image z **čistého, rozbaleného Raspberry
Pi OS Lite ARM64**. Vstupní SHA-256 je povinný; nic nestahuje ani nevolí pohyblivou
„latest“ verzi. Build vyžaduje vyhrazený Linux ARM64 host s rootem, loop zařízeními,
util-linux, e2fsprogs a přístupem k balíčkovým repozitářům. Na Apple Silicon macOS
je podporované sestavení přes Docker Desktop a `scripts/image/build-docker.py`.
Build neprobíhá na provozní bráně.

## Výstup a bezpečnost

Builder přijímá jen běžný soubor a nový výstup, odmítne existující výstup, symlink,
neshodný checksum a nepodporované rozdělení disku. Podporovaný vstup má MBR,
512b sektory, boot FAT a druhý ext4 root oddíl. Kopii rozšíří: root získá 2 GiB
pro balíčky, následuje samostatný 2GiB ext4 oddíl `GWDATA`. Fyzická zařízení nejsou
povoleným vstupem ani cílem partition operací.

Do obrazu instaluje závislosti, čtyři ARM64 nástroje gatewaye, Ansible playbooky
a first-boot službu. Nemaže ani neformátuje vstup. Výstup po chybě není hotový image;
nepřepisujte jej při opakování, zvolte nový název po kontrole logu.

Obraz neobsahuje identitu gatewaye, servisní heslo, zákaznickou Wi-Fi, cloudový
token, SSH host klíče ani webový certifikát. Nevytvářejte jej exportem zákaznické VM.
Builder odmítá nalezenou gateway DB a síťové profily v základním obrazu.
Balíčkový repozitář není připnutý snapshot: hash výstupního artefaktu je evidovaný,
ale byte-for-byte reprodukovatelnost vyžaduje vlastní snapshot APT repozitáře.

## Sestavení

Nejprve sestavte čtyři nástroje přes `make arm64 VERSION=<verze>`.
Příklad pro přípravný Linux host:

```sh
sudo ansible-playbook deploy/ansible/image.yml \
  -e gateway_base_image=/build/raspios-lite-arm64.img \
  -e gateway_base_sha256=<sha256-rozbaleneho-obrazu> \
  -e gateway_output_image=/build/stitkovac-gateway.img
```

Nezapisující kontrola vstupu:

```sh
python3 scripts/image/build.py --base /build/raspios-lite-arm64.img \
  --sha256 <sha256-rozbaleneho-obrazu> --output /build/stitkovac-gateway.img --validate-only
python3 scripts/image/test_image.py
```

Sestavení na Macu s Docker Desktop (stejné argumenty fungují i na ARM64 Linuxu):

```sh
python3 scripts/image/build-docker.py \
  --base .cache/os-image/raspios-trixie-arm64-lite.img \
  --sha256 49fafba626ec00e0f9800349b9edae6caf8cfc223f673b875b72ac2797ed9576 \
  --output dist/rpi/stitkovac-gateway-0.1.8-arm64.img \
  --release dist/releases/0.1.8
```

Adresář vydání obsahuje jen `gateway`, `manifest.json` a `public-keys.json`.
Builder ověřuje podpis i shodu s ARM64 binárkou. Do kontejneru nepřipojuje
privátní podpisový klíč, konfiguraci laboratoře ani cloudové přístupy. Loop
zařízení vyžadují privilegovaný kontejner; zapisuje se do nového souboru image.
Při chybě se oddíly odpojí před úklidem mountpointu, nikdy se nemaže jejich obsah.

Výstupní `.json` eviduje hash základu, výsledného obrazu a architekturu.
Verze 0.1.8 byla sestavena pro Raspberry Pi 3 Model B+ (1 GB RAM) ze základu
Raspberry Pi OS Lite ARM64 Trixie z 15. 9. 2026. SHA-256 rozbaleného základu
byl ověřen proti [oficiálnímu katalogu Raspberry Pi](https://downloads.raspberrypi.com/os_list_imagingutility_v4.json).
Distribuční artefakty a návod jsou v ignorovaném `dist/rpi/`.
Fyzická SD karta, Wi-Fi a tiskárna vyžadují přejímku na cílovém hardwaru.

Ověření 30. 9. 2026: šest kontrolních testů builderu/přípravy karty, syntaxe
Ansible a skutečný provisioning v QEMU `raspi3b` s 1 GB RAM. Test zahrnul
zachování identity při opakování po chybě, vytvoření síťové konfigurace,
inicializaci podepsaného OTA 0.1.8, odstranění provisioning seeda a průchod
produkčním storage guardem po druhém bootu. Odhalil a opravil rozlišení
`LoadState=not-found` v Ansible místo pouhé přítomnosti názvu služby.

Test je reprodukovatelný přes `scripts/image/Dockerfile.smoke` a
`scripts/image/smoke-boot.py`. Používá vlastní kopii disku a neveřejnou testovací
identitu. QEMU nemá Wi-Fi a jeho watchdog není kompatibilní s disarmingem
aktuálního vendor initramfs; vypíná se proto pouze v testovacím DTB. Testovací
harness ukončuje emulaci po dokončeném shutdownu a druhý boot spouští znovu.
Distribuční DTB zůstává původní; skutečný hardwarový reset ani síťové periferie
tímto testem nejsou označené za ověřené.

Vendor cloud-init, automatické zvětšení oddílů, interaktivní první přihlášení
a automatické EEPROM aktualizace jsou vypnuté. Trixie swap používá pouze
256 MiB zram bez zápisu na SD; síťová inicializace nastaví zemi a odblokuje Wi-Fi.

## První boot konkrétní karty

Na **už nahranou kartu konkrétní brány** vložte do boot oddílu
`gateway-provision.json`. Neukládejte personalizovanou kartu jako distribuční image.
Soubor obsahuje přesně tři položky:

```json
{
  "admin_password": "unique-secret-from-your-password-manager",
  "ssh_public_key": "ssh-ed25519 PUBLIC_KEY_FROM_TECHNICIAN",
  "country": "CZ"
}
```

Pro pohodlnou přípravu jedné karty použijte `scripts/image/prepare-card.py`.
Vytvoří nové náhodné heslo a SSH klíč, soukromé přístupy uloží pouze na počítači
a na boot oddíl zapíše heslo s veřejným klíčem. Odmítne přepsat existující seed
i adresář přístupů a ověří značku `gateway-image.json` v boot oddílu.

```sh
python3 scripts/image/prepare-card.py /Volumes/bootfs \
  --access-dir "$HOME/gateway-pristupy-provozovna-1"
```

Heslo má nejméně 16 bajtů, SSH klíč musí být jeden Ed25519 veřejný klíč.
Služba přesune provisioning do privátního `/data`, spustí lokální Ansible bootstrap
a síťový playbook bez instalace dalších balíčků, vytvoří unikátní identitu,
TLS certifikát, SSH host klíč, machine-id a servisní AP. Instalace nevyžaduje
zákaznickou Wi-Fi; její údaje nastaví technik později v servisním webu.

Poté uloží trvalou konfiguraci, přepne root a boot na read-only při dalším bootu,
nastaví RAM logy a dočasné adresáře, odstraní bootstrap heslo a restartuje zařízení.
Persistentní části jsou `/data`, profily/stav NetworkManageru a stav systemd.
Již inicializovaná databáze se při opakování nepřepisuje. Neúplná inicializace
nepovolí spuštění tiskové služby. Chyba první inicializace může vyžadovat opravu karty na přípravném počítači;
pokud vypadne napájení, zůstává privátní seed pro opakování, nikoli univerzální heslo.
Dokončení je zapsané atomicky až po flush systémové konfigurace.
Při chybě journal uvádí původní řádek inicializačního skriptu. Posledních nejvýše
64 KiB výstupu neúspěšného příkazu zůstává v root-only
`/data/gateway-bootstrap-error.log`; úlohy Ansible s hesly používají `no_log`.
Po úspěšném opakování se chybový log odstraní.

SSH je pouze pro účet `technik` s klíčem. Jeho autorizační soubor je
`/data/access/authorized_keys`, SSH host klíč v `/data/ssh`. Servisní AP má vlastní
náhodné heslo v root-only `/data/network/ap-credentials.json`; technik si jej
převezme přes servisní ethernet/konzoli a uloží do správce hesel. Cloudový token
se přidává až při registraci konkrétního zařízení.

## Přejímka image

Na podporovaném RPi ověřte druhý boot, `check-storage`, izolaci ethernetu/Wi-Fi,
přístup servisním klíčem a heslem, unikátní identity na dvou kartách, DHCP a reálný
PDF tisk. Následuje série vypnutí napájení během idle, tisku a prvního provisioningu.
Automatické aktualizace základního OS jsou vypnuté. S volbou `--release` se
aplikační OTA inicializuje automaticky z přibaleného podepsaného vydání přes
`deploy/ansible/ota.yml`. Image 0.1.8 používá dosavadní pilotní veřejný klíč
`virtualbox-lab-20260929`; privátní klíč v image není. Bez této volby zůstává
OTA vypnuté a vyžaduje samostatný servisní provisioning.

## Pojmenování při dalším sestavení

Nově sestavený image při prvním bootu nastaví hostname a servisní SSID podle
posledních dvou bajtů trvalé Wi-Fi MAC, například `stitkovac-gw-9a-e3`.
Podrobnosti a servisní přejmenování existujících zařízení:
[pojmenování brány](device-naming.md). Dříve sestavený image 0.1.8 tato změna neupravuje.
