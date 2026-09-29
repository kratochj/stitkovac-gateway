# ADR 001: Trvalé rezervace před DHCP odpovědí

Stav: přijato při první implementaci.

Původní návrh předpokládal dnsmasq s lease hookem. Hook spuštěný po DHCP ACK
však nemůže sám zajistit, že přidělení přežije odpojení napájení mezi odpovědí
tiskárně a uložením rezervace.

První implementace proto používá malou integrovanou DHCPv4 službu. MAC/IP
rezervaci potvrdí SQLite transakcí ještě před sestavením OFFER a aktivní lease
uloží před ACK. Při chybě úložiště se úspěšná odpověď neposílá. Rezervují se i
adresy nabízené při DISCOVER, takže nabídka jiné adresy po restartu není nutná.
Nevyužité rezervace se samy neuvolňují.

Rozsah je záměrně omezen na dedikovaný ethernetový subnet: DISCOVER, REQUEST,
renew/rebind, RELEASE a DECLINE. Relay, BOOTP, DHCPv6 a option overload se
nepodporují. Router ani DNS options se neposílají. Client identifier se pro
přiřazování ignoruje, ale v odpovědi se vrací. DECLINE trvale označí konflikt
a zablokuje danou rezervaci do servisního řešení.

Linuxový socket používá `SO_BINDTODEVICE` a před startem ověří adresu rozhraní.
Na uplinku nesmí běžet. Ansible nesmí vedle něj spustit ethernetový dnsmasq.
Pro dočasné servisní AP může později běžet samostatný DHCP server.

Testy ověřují přežití rezervace po znovuotevření DB, změny client identifier,
RELEASE/DECLINE, neplatné pakety a zákaz odpovědi při chybě persistence.
Fyzický power-cut test, kompatibilita PC42E a ochrana před cizí statickou adresou
v poolu jsou stále povinné před produkčním nasazením.

Podklady: [RFC 2131](https://www.rfc-editor.org/rfc/rfc2131),
[RFC 2132](https://www.rfc-editor.org/rfc/rfc2132).
