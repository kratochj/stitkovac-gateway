# Gateway API v1: první implementovaný klient

Stav: implementovaný klient a integrační test s TLS/WSS serverem. Produkční
`stitkovac-server` tyto endpointy zatím neimplementuje. Nezapínat cloudové
připojení na zákaznické bráně před dokončením serverové části a pilotu.

Všechny cesty začínají `/api/gateway/v1`. Každý request má Bearer token brány.
Po WSS handshake mají HTTP requesty pro úlohy také `X-Gateway-Session`.
Server ověřuje token, organizaci, přiřazenou bránu a aktuální session.
URL musí být HTTPS origin bez uživatelských údajů, query nebo path prefixu.
Redirecty nejsou povolené.

## WebSocket

`GET /connect` provede upgrade. Klient pošle `hello` s `version: 1`,
`messageId`, `gatewayId` a `agentVersion`. Server odpoví `ready` se stejnou
verzí protokolu a unikátním `sessionId`. Deadline handshake je 10 sekund.

Po `ready` klient jednorázově synchronizuje frontu. Zpráva `jobs.available`
s `version: 1` a `messageId` vyvolá další synchronizaci. Více současných
notifikací se sloučí. Událost je upozornění; pořadí práce určuje HTTP fronta.
Klient posílá WebSocket ping každých 20 sekund s desetisekundovým timeoutem.
Po chybě se připojí znovu s jitterem a backoffem nejvýše 30 sekund.

Přijetí WebSocket zprávy není potvrzení dokončení úlohy. Server musí dál
signalizovat nepotvrzenou práci a po reconnectu ji vrátit ve frontě.

## HTTP tiskové operace

| Metoda a cesta | Úspěšná odpověď | Sémantika |
|---|---|---|
| `GET /jobs?cursor=...` | 200, `jobs` jako seznam UID a `nextCursor` | Stabilní pořadí a cursor; pending i vlastní neuzavřené pokusy |
| `POST /jobs/{uid}/claim` | 200, metadata pokusu | Atomické převzetí, opakovaný claim stejné brány vrací stejný attempt |
| `GET /jobs/{uid}/document?attemptId=...` | 200, původní bajty | Jen vlastník pokusu; bez přesměrování na libovolnou URL |
| `POST /jobs/{uid}/start` | 204 | Tělo `attemptId`; idempotentní povolení konkrétního pokusu |
| `POST /jobs/{uid}/result` | 204 | Tělo `attemptId`, `state`, `reason`; trvale přijatý idempotentní výsledek |

Metadata pokusu obsahují `jobUid`, `attemptId`, `mac`, `ip`, `port`, `digest`
(lowercase SHA-256 hex) a `expiresAt` (Unix sekundy). UID a attempt ID obsahují
jen písmena ASCII, číslice, podtržítko a spojovník, nejvýše 128 znaků.
Gateway porovná cílovou MAC/IP s vlastní trvalou rezervací.

Jedna stránka obsahuje nejvýše 100 úloh a 16 MiB dokumentů celkem. Dokument
má nejvýše 8 MiB, JSON odpověď 256 KiB, WSS zpráva 16 KiB. Server nesmí
stránkování založit na offsetu fronty měněné potvrzováním předchozí stránky.

Klient zpracovává nejvýše čtyři fyzické endpointy současně, pro každý zachovává
pořadí. Další stránka nepředbíhá předchozí. Chyba synchronizace vyvolá reconnect
a nové sjednocení stavu; nepotvrzený `SENT` se znovu hlásí bez TCP zápisu.
Dokumenty pro dosud serverem neuzavřené pokusy musejí zůstat dostupné i při
opakovaném claimu; po trvalém přijetí výsledku už server úlohu nevrací.

`SENT` označuje předání bajtů, `UNKNOWN` možný částečný přenos, `FAILED`
prokazatelný neúspěch před zápisem a `EXPIRED` neprovedený starý pokus.
Nový tiskový pokus nesmí vzniknout automaticky z `UNKNOWN`.

## Co zbývá v serveru

Registrace a rotace tokenů, session fencing, tenant izolace, trvalý outbox,
claim/start/result, cursor fronty, oddělení od LOCALNET a superadmin přehled.
Před release doplnit kontraktní testy proti skutečnému Spring backendu.
