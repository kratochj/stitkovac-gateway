# Administrace přes zákaznickou Wi-Fi

Od verze 0.1.9 lze na stránce **Síť** povolit přístup do administrace přes
zákaznickou Wi-Fi. Výchozí stav je vypnutý. HTTPS používá port 8443 a stejné heslo
jako přístup po kabelu. SSH se na zákaznické Wi-Fi nepovoluje.

Agent otevře samostatný listener pouze na aktuální privátní IPv4 adrese ověřeného
uplinku. Nepoužívá wildcard adresu. Změnu DHCP adresy převezme bez restartu tisku;
listener kontroluje stav pomocníka každé dvě sekundy. Pomocník obnovuje stav linky
běžně každých pět sekund, takže změna adresy se může projevit s krátkým zpožděním.
Při vypnutí se listener a jeho spojení uzavřou. Každý HTTP požadavek navíc ověřuje
aktuální povolení, cílovou adresu socketu a Host; při nedostupnosti pomocníka
přístup odmítne. Ochrana Origin, přihlášení a CSRF zůstává zachovaná.

Volba je atomicky uložená v `/data/network/admin-access.json` s režimem 0600.
Přežije restart i výpadek napájení a nezasahuje do uloženého profilu Wi-Fi.
Ve zkušebním připojení, servisním AP, simulaci nebo při kolizi s tiskovým či
servisním subnetem se nový listener nespouští. Ethernet i servisní AP používají
své dosavadní listenery. Starý pomocník funkci nehlásí a nový agent ji nezpřístupní.

Firewall dovoluje IPv4 TCP 8443 na Wi-Fi k jiným cílovým adresám než adrese tiskové
sítě a servisního AP. Skutečné zpřístupnění řídí existence listeneru a kontrola
každého požadavku. Zákaznická izolace klientů může přístup stále blokovat.
Certifikát zůstává vlastní; při otevření Wi-Fi IP může prohlížeč požadovat novou
výjimku také kvůli jménu/adrese certifikátu. Nedochází k automatickému vystavení
veřejně důvěryhodného certifikátu ani k otevření portu v zákaznickém routeru.

## Aktualizace existujícího Pi

Aplikační OTA neumí měnit systémový firewall ani root pomocníka. Jednorázově je
potřeba spustit `deploy/ansible/wifi-admin.yml` přes SSH po Ethernetu. Další změny
samotného webu a agentu už lze doručovat běžným OTA. Nové image při sestavení
převezmou změněný firewall i helper ze stejného repozitáře; již vydaný image 0.1.8
se nemění.

Předpoklady: inicializovaný ARM64 appliance image, read-only root/boot, platný
SSH klíč technika a dokončený tisk. Playbook odmítne probíhající OTA. Použije
stávající síťové adresy a existující veřejné podpisové klíče. Nepřepisuje hesla,
SSH klíče, serverový token, rezervace, identitu ani tiskovou historii.

Playbook připraví soubory, ověří syntaxi firewallu, uloží původní helper a firewall
do `/data/service-backups/wifi-admin-0.1.9`, zastaví agent a ověří podepsaný
balíček. V krátké servisní fázi přepne root do zápisu, atomicky nahradí systémové
soubory a v bloku `always` vrátí root do read-only. Poté načte firewall a spustí
helper i aplikační trial přes dosavadní launcher. Kontroluje potvrzení verze.
Launcher při neúspěšném aplikačním trialu obnoví předchozí aplikaci; systémové
soubory tento mechanismus nevrací.

Při chybě servisní operace neopakujte tisk naslepo. SSH po Ethernetu zůstává
povolené. Zkontrolujte výstup Ansible a stav služeb. Pokud selže instalace
systémových souborů, lze během zastaveného agentu a pomocníka vrátit oba soubory
ze zálohy, obnovit read-only root a znovu načíst firewall. Nedokončený servisní
upgrade může ponechat služby zastavené; playbook úmyslně nehlásí úspěch.

## Ověření

Automatické testy pokrývají trvalé zapnutí/vypnutí, chybu ukládání, starého nebo
nedostupného pomocníka, odmítnutí cizího Host a Origin, ochranu CSRF, zánik a
znovuvytvoření listeneru při změně DHCP adresy a ukončení procesu. Linux test
kontroluje peer UID na Unix socketu, obnovu nastavení po pádu helperu a skutečnou
syntaxi/aplikaci nftables v izolované síti. Ověření konkrétního Pi po instalaci
musí zahrnout přihlášení přes jeho Wi-Fi adresu a následné vypnutí přístupu.

## Fyzické ověření 30. 9. 2026

Servisní playbook úspěšně aktualizoval pilotní Pi 3 B+ z 0.1.8 na 0.1.9.
Launcher potvrdil aktivní 0.1.9 a zachoval předchozí 0.1.8; agent, síťový pomocník
a nftables běží. Storage guard prošel, root i boot zůstaly read-only.
Uživatel následně sám zapnul Wi-Fi administraci a potvrdil funkční přístup.
Automatický HTTP test na tomto Pi se nespouštěl; Host/Origin/CSRF a přepínání
listeneru jsou ověřené automatickými lokálními testy.

Při prvním pokusu zastavil instalátor přípravu před odstavením služeb, protože
`/run` má na image příznak `noexec`. Finální balíček používá rootem vlastněný
staging v `/data/gateway-service-stage`, který po instalaci odstraní.
