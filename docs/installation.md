# Příprava a ověření instalace

Tento postup je pro vývojovou bránu. První implementace nemá hotový provisioning
Wi-Fi ani servisní AP. Serverové endpointy a webová registrace jsou implementované
a nasazené na `cloud.stitkovac.app` ve verzi **2.7.12** od 2026-09-29.

## Předpoklady

- Raspberry Pi OS Lite 64-bit s připnutou verzí a známým checksumem obrazu.
- Samostatně připravený ext4 oddíl `/data`, připojovaný při bootu.
- Existující technický účet s SSH klíčem a sudo; Ansible běží z počítače technika.
- Wi-Fi uplink a soukromý ethernetový profil nastavené předem přes NetworkManager.
- Adresa ethernetu musí odpovídat `gateway_printer_address` a prefixu v inventory.
- V tiskovém ethernetovém subnetu neběží jiný DHCP server ani ručně nastavené
  tiskárny v DHCP poolu. Ethernet není propojen do zákazníkovy LAN.
- Tiskárny mají zapnuté DHCP. Tisková síť nemá výchozí trasu ani sdílení uplinku.

Ansible bootstrap vyžaduje už existující `/data`; neformátuje žádné disky.
Instaluje se při přípravě zapisovatelného OS, nikoli na aktivní zákaznické bráně.
Vytvoří služební účet, uloží binární soubor a inicializuje identitu, jen pokud
DB dosud neexistuje. Chybnou nebo neúplnou DB neodstraňuje automaticky.

## Build a provisioning

```sh
make arm64
ansible-playbook -i deploy/ansible/inventory.example.yml deploy/ansible/bootstrap.yml --syntax-check
ansible-playbook -i /path/to/private-inventory.yml deploy/ansible/bootstrap.yml --ask-vault-pass -e @/path/to/vault.yml
```

Privátní inventory obsahuje adresu a SSH účet cílového zařízení. Vault poskytuje
`gateway_admin_password`; před prvním provisionováním jej technik uloží do svého
správce hesel. Dočasný soubor s heslem existuje jen v `/run` a playbook jej uklidí.
Opakované spuštění nemění existující heslo, identitu, rezervace ani runtime argumenty.

Bootstrap nesmí běžet při aktivním tisku. Běžný upgrade přes přepis systémových
souborů se nepoužívá. Podepsané verzované releases, launcher a servisní
updater popisuje [stav OTA implementace](ota-implementation.md); automatické
ovládání přes server a provisioning klíčů ještě nejsou propojené.

## Read-only systém a aktivace

Na konkrétním image je nutné před aktivací ověřit boot/system read-only režim,
RAM OverlayFS, oddělený `/data`, trvalé síťové profily a vypnutý diskový swap.
Tato konfigurace ani dělení SD zatím nejsou automatizované. Po uzamčení systému
služba kontroluje filesystem layout před každým startem:

- `/data` je samostatný zapisovatelný ext4 mount a obsahuje inicializovanou DB.
- Root je read-only, případně OverlayFS s RAM upper a read-only lower.
  Guard kontroluje superblock přes `findmnt FS-OPTIONS`, aby nestačilo pouze
  read-only připojení uvnitř sandboxu služby.
- Boot filesystem je read-only.

Neúspěšná kontrola zastaví službu; nikdy nenahradí chybějící data prázdnou DB.
Nejprve na přípravné kartě povolte službu přes `systemctl enable stitkovac-gateway`,
potom dokončete read-only konfiguraci a restartujte. Stav lze read-only ověřit:

```sh
ansible-playbook -i /path/to/private-inventory.yml deploy/ansible/diagnostics.yml
```

Na zákaznické Wi-Fi nemá být přístupný servisní HTTPS port 8443 ani SSH. Web
se explicitně váže na IP tiskové sítě a DHCP na její Linux rozhraní. Hostitel
potřebuje také ověřenou firewall politiku; bootstrap ji zatím nemění.
Servisní AP a fyzické tlačítko ještě nejsou implementované.

## Debian balíček

Na Linuxu s `dpkg-deb`:

```sh
make arm64
sh scripts/package.sh 0.1.0-dev
```

Balíček obsahuje ARM64 agenta, launcher, updater, storage guard a systemd unit.
Výchozí služba zatím používá přímo agenta; podepisovací nástroj patří pouze
na release stanici a není v balíčku. Samotná
instalace `.deb` netvoří plně připravenou bránu: účet, runtime konfigurace,
úložiště a inicializace jsou odpovědností provisioningu. Balíček službu sám
nespouští, neobsahuje hesla, tokeny ani univerzální identitu.

## Připojení testovacího cloudu

Volitelné přepínače `--cloud-url` a `--cloud-token-file` vyžadují také
`--dhcp-interface`. Token je v samostatném souboru s režimem 0600 a patří
konkrétní bráně. URL musí poskytovat [protokol v1](protocol-v1.md).
Produkční server od 2.7.12 protokol poskytuje; použijte token vytvořený
v administraci bran. Běžný uživatelský JWT není tokenem brány.

Integrační test v `internal/cloud` používá vlastní TLS server a simuluje tiskové
spojení. Ověřuje dispatch a duplicity, nikoli hardwarovou kompatibilitu.
Před zákaznickou instalací dokončit test proti PC42E,
firewall, změnu Wi-Fi a sérii fyzických power-cut zkoušek podle specifikace.
