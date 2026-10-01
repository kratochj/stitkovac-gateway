# Integrita platformy a persistentní diagnostika

Před každým startem `stitkovac-gateway.service`, včetně automatického restartu po pádu,
proběhne kontrola SHA-256 launcheru a updateru. Aplikační OTA nadále ověřuje podepsané
release vlastním mechanismem; tato kontrola chrání dvě stabilní platformní binárky.

## Kontrola a obnova

Instalace uloží do `/usr/lib/stitkovac-gateway/recovery.json` SHA-256 vypočtené z důvěryhodných
vstupů na instalačním počítači a odpovídající kopie do podadresáře `recovery/`, pojmenované
kontrolním součtem. Manifest a kopie patří rootovi a za běžného provozu jsou na read-only rootu.
OTA provisioning aktualizuje kopie a manifest společně s platformními binárkami;
běžná aktualizace aplikace je nemění. Manifest se publikuje až po instalaci obou kopií.

Preflight nejprve ověří rozložení úložiště. Zdravé binárky nevyvolají žádný zápis na root.
Při odchylce ověří všechny potřebné náhradní kopie ještě před přepnutím rootu do zápisu.
Obnova podporuje read-only ext4 root: uloží náhradu přes dočasný soubor, `fsync`, atomické
přejmenování a `fsync` adresáře. Ověří výsledek a vrátí root do režimu pouze pro čtení.
Následný storage guard musí znovu projít, jinak agent nenastartuje.

Vadná nebo chybějící záloha, vadný manifest, chyba zápisu či neúspěšné uzamčení rootu
zastaví start služby. Po chybě zápisu se vždy pokusí root uzamknout. Kontrolní skript běží
jako privilegovaný `ExecStartPre=+`, samotný agent zachovává původní omezení oprávnění.
Význam tohoto prefixu popisuje [systemd](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html#Command%20lines).

Toto není ochrana proti kompromitovanému rootovi ani náhrada zdravé SD karty. Záložní kopie
jsou na stejném médiu; při poškození Pythonu, kontrolního skriptu, manifestu nebo obou kopií
je nutný servisní zásah. Výpadek napájení během obnovy zanechá buď původní, nebo novou
binárku; při dalším bootu proběhne kontrola znovu. Žádný software nezaručí atomické chování
vadného média. Poškozené binárky se automaticky nearchivují, ukládají se jejich kontrolní součty.

## Diagnostika

Root-only `/data/gateway-diagnostics/events.json` (adresář 0700, soubor 0600) uchovává odděleně:

- posledních 32 událostí kontroly integrity a obnovy,
- posledních 128 diagnostických snímků, běžně přibližně 10 hodin provozu.

Soubor má limit 256 KiB, nahrazuje se atomicky a synchronizuje na disk. Snímky vznikají
při preflightu, ukončení služby a časovačem každých 5 minut (poprvé po 2 minutách bootu).
Obsahují boot ID, uptime, systémový čas, stav služby, exit status, počet restartů,
`get_throttled`, je-li dostupný, a počty vybraných kategorií chyb v posledních 200 zprávách
kernel journalu aktuálního bootu. Počty nejsou celkové počty chyb a jednotlivé snímky se
mohou překrývat. Uptime a boot ID umožňují rozlišit starty i před synchronizací času.

Neukládá se prostředí procesu, obsah konfigurace, aplikační log, tiskové dokumenty ani
volný text zpráv jádra. Systémový journal zůstává v RAM. Periodický snímek může minout
chybu těsně před odpojením napájení; bez napájení nelze záznam dokončit. Chyba diagnostického
zápisu sama neblokuje tisk. Chybějící ext4 `/data` se nesmí nahradit zápisem na root filesystem.

## Instalace a servis

Nové image dostanou mechanismus přes `bootstrap.yml`, OTA provisioning aktualizuje stejné
podklady. Debian balíček obsahuje guard, kopie, manifest, drop-in a časovač; stejně jako
ostatní služby se jeho aktivace provádí v rámci provisioningu připraveného zařízení.

Pro existující Pi je určen `deploy/ansible/platform-health.yml`. Běží po zastavení agenta
i síťového helperu, s `gateway_platform_maintenance=true` a explicitními cestami
`gateway_launcher_binary` a `gateway_update_binary` k důvěryhodným binárkám na instalačním
počítači. Jejich SHA-256 musí odpovídat instalované platformě; tento playbook neprovádí
implicitní upgrade ani opravu neznámé předchozí korupce. Root odemkne jen po dobu instalace,
potom jej zamkne a guard ověří. Agenta ani helper automaticky nespouští. Časovač diagnostiky
aktivuje. Aplikační verze, tokeny, identita a tisková historie se nemění.

Diagnostiku lze přečíst přes servisní SSH pomocí `sudo cat /data/gateway-diagnostics/events.json`.
Běžný aplikační OTA balíček tuto platformní změnu na již existující bránu nenainstaluje.
