# Lokální servisní rozhraní

Účel první obrazovky: technik pozná identitu brány a zařízení, kterým přidělila
adresy. Rozhraní poskytuje přihlášení, rezervace, serverové připojení, diagnostiku
tisku a od 0.1.4 také [síťovou administraci](network-administration.md).

Vizuální návrh: bílé pracovní plochy `#ffffff`, světlé pozadí `#eef2f5`,
tmavě modrý text `#19334a`, modrá akce `#155fa0`, zelený stav `#23613e`
a červená chyba `#a5282d`. Písmo lokálně dostupné Arial bez stahování fontů,
adresy ve standardním monospace pro snadné porovnání znaků.

Rozložení je zarovnané vlevo: úzký horní pruh s názvem a odhlášením, hlavní
identita brány, jeden stavový panel a tabulka DHCP zařízení. Na telefonu má
tabulka vlastní horizontální posuv; ovládací prvky mají alespoň 44 px.

Kontrola návrhu: servis nepotřebuje hero, marketingové texty, grafy ani sadu
statistických karet. Důraz proto nese tabulka adres a slovní rozlišení rezervace,
aktivní lease a konfliktu. Barva není jediným nositelem stavu. Bez animací
a externích zdrojů funguje rozhraní i bez internetu.
