# Název zařízení a servisní Wi-Fi

Hostname i SSID servisní sítě mají shodný formát `stitkovac-gw-xx-yy`.
Přípona jsou poslední dva bajty **trvalé MAC Wi-Fi adaptéru**, malými písmeny
včetně úvodních nul. Například `b8:27:eb:e3:9a:e3` vytvoří
`stitkovac-gw-9a-e3`; koncovka `00:01` vytvoří `stitkovac-gw-00-01`.

Hostname používá DNS-kompatibilní pomlčky. Zkrácená přípona nemusí být globálně
unikátní; cloudová identita brány se nemění a dál používá vlastní ID.
Změna klientské Wi-Fi MAC při randomizaci nemění odvozené jméno.

## Nová zařízení

Ansible `network.yml` před změnou konfigurace zjistí permanentní MAC přes
`ip -j link`. Pokud kernel nenabídne `permaddr`, přijme adresu ze sysfs pouze
tehdy, když ji kernel označuje jako permanentní (`addr_assign_type=0`).
Náhodnou nebo ručně přepsanou adresu bez známé permanentní MAC odmítne.

Provisioning nastaví hostname přes systemd, lokální překlad v `/etc/hosts`
a zakáže přepisování hostname z uplink DHCP. Shodný název uloží do konfigurace
síťového pomocníka i přístupových údajů AP. Při opakovaném provisioningu zachová
heslo a UUID AP. Nové heslo se vytvoří jen pro dosud neinicializovanou servisní síť.

Toto platí pro nově sestavené image; již vytvořený image 0.1.8 se zpětně nemění.
QEMU nemá Wi-Fi rádio, proto smoke test pouze v pracovní kopii image explicitně
vloží testovací MAC přes `gateway_naming_test_mac`. Tato proměnná není součástí
produkčního image ani uživatelského provisioning souboru. Smoke ověřuje shodu
hostname a obou uložených SSID; nejde o test skutečného rádiového vysílání.

## Existující gateway

Jednorázová změna vyžaduje servisní přístup přes Ethernet; samotné aplikační OTA
nemění hostname ani systémovou konfiguraci AP. Použij `deploy/ansible/device-name.yml`.

1. Dokonči tisk a vypni servisní AP, je-li aktivní. Připoj se přes tiskový Ethernet.
2. Zastav `stitkovac-gateway` a `stitkovac-gateway-network`.
3. Spusť playbook se stávajícím inventory a SSH klíčem:

```sh
ansible-playbook -i inventory.yml deploy/ansible/device-name.yml \
  --private-key /path/to/technik_ed25519 -e gateway_name_maintenance=true
```

4. Spusť `stitkovac-gateway-network` a `stitkovac-gateway` a ověř hostname.

Playbook odmítne běžící služby i aktivní AP. Použije Wi-Fi rozhraní z existující
konfigurace, přejmenuje zařízení a SSID, ale zachová ostatní konfiguraci, hesla,
UUID, serverový token, identitu a tiskovou historii. Po zápisu obnoví read-only
root i při chybě. Při chybě ponech služby zastavené a ověř výstup před pokračováním.

Nový SSID se skutečně odvysílá při příští aktivaci servisního AP. Uplink DHCP
pošle nový hostname při příštím získání lease; stávající záznam v routeru se
nemusí změnit okamžitě. Playbook nevyvolává přepnutí Wi-Fi ani tisk.
