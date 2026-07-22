<div align="center">

# DroidMCP

**Servidores Model Context Protocol nativos para Android y Termux**

Binarios Go autocontenidos que le dan a cualquier cliente MCP — Claude Code, Gemini CLI o el tuyo propio —
manos sobre un dispositivo Android. Sin Node.js, sin Python, sin runtime que instalar.

[![CI](https://img.shields.io/github/actions/workflow/status/kahz12/DroidMCP/build.yml?branch=main&style=flat-square&label=CI)](https://github.com/kahz12/DroidMCP/actions/workflows/build.yml)
[![Release](https://img.shields.io/github/v/release/kahz12/DroidMCP?style=flat-square)](https://github.com/kahz12/DroidMCP/releases/latest)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white)](go.mod)
[![Platform](https://img.shields.io/badge/Android%20%C2%B7%20Termux-ARM64-3DDC84?style=flat-square&logo=android&logoColor=white)](https://termux.dev)
[![License](https://img.shields.io/badge/license-MIT-blue?style=flat-square)](LICENSE)

[English](README.md) · [Español](README.es.md) · [Guía de uso](docs/usage.es.md) · [Seguridad](docs/security.md) · [Roadmap](ROADMAP.md)

</div>

---

## De un vistazo

| Cero dependencias | Seguro por defecto | Diez servidores enfocados |
|:---|:---|:---|
| Un binario ARM64 estático por servidor, Go puro — sin CGO, sin intérprete, nada más que instalar. | Listener solo en loopback, autenticación por API key, TLS opcional, raíces en sandbox, logs redactados, releases firmadas. | Archivos, GitHub, web scraping, shell, LAN, portapapeles, medios, SQLite, sensores del dispositivo y notificaciones — cada uno tras una superficie de tools pequeña y auditable. |

```
   Claude Code · Gemini CLI · cualquier cliente MCP
                          │
                          │  Protocolo MCP sobre HTTP/SSE
                          ▼
       ┌──────────────────────────────────────┐
       │      DroidMCP — Termux · ARM64       │
       ├────────────┬────────────┬────────────┤
       │ filesystem │   github   │  scraper   │
       ├────────────┼────────────┼────────────┤
       │   termux   │  network   │ clipboard  │
       ├────────────┼────────────┼────────────┤
       │   media    │   sqlite   │  sensors   │
       ├────────────┴────────────┴────────────┤
       │            notifications             │
       └──────────────────────────────────────┘
```

## Servidores

| Servidor | Puerto | Enfoque | Requiere |
|----------|:---:|---------|----------|
| `mcp-filesystem` | `3000` | Operaciones de archivos en sandbox, con protección contra path traversal | `DROIDMCP_ROOT` + key |
| `mcp-github` | `3001` | API completa de GitHub vía Personal Access Token | `GITHUB_TOKEN` + key |
| `mcp-scraper` | `3002` | Web scraping y extracción sin Chromium | — |
| `mcp-termux` | `3003` | Ejecución de shell y gestión de paquetes | key |
| `mcp-network` | `3004` | Descubrimiento de LAN y escaneo de puertos | — |
| `mcp-clipboard` | `3005` | Puente del portapapeles de Android vía Termux:API | `termux-api` |
| `mcp-media` | `3006` | Navegación de medios y transformaciones con `ffmpeg` | `DROIDMCP_ROOT` + key |
| `mcp-sqlite` | `3007` | Bases de datos SQLite locales, Go puro — sin CGO | `DROIDMCP_ROOT` + key |
| `mcp-sensors` | `3008` | Sensores del dispositivo: batería, ubicación, WiFi, brillo, volumen | `termux-api` |
| `mcp-notifications` | `3009` | Notificaciones de Android y estado de No molestar | `termux-api` |

Despliega un servidor para ver su lista de tools; la referencia completa por
tool, con argumentos y ejemplos, está en la [guía de uso](docs/usage.es.md).

<details>
<summary><b>mcp-filesystem</b> — operaciones de archivos seguras dentro de una raíz configurable</summary>
<br>

| Tool | Descripción |
|------|-------------|
| `read_file` | Lee el contenido de un archivo |
| `read_file_lines` | Lee un rango de líneas de un archivo |
| `write_file` | Escribe o crea un archivo (crea directorios padre) |
| `list_directory` | Lista el contenido de un directorio con tipo y tamaño |
| `stat` | Metadatos de archivo: tamaño, modo, fechas, propietario |
| `search_files` | Búsqueda recursiva de archivos con patrones glob |
| `delete_file` | Elimina un archivo o directorio vacío |
| `move_file` | Mueve o renombra un archivo/directorio |
| `copy_file` | Copia un archivo |

</details>

<details>
<summary><b>mcp-github</b> — operaciones completas de GitHub, sobre <code>google/go-github</code></summary>
<br>

| Tool | Descripción |
|------|-------------|
| `list_repos` · `get_repo` · `fork_repo` | Navega y haz fork de repositorios |
| `list_branches` · `list_tags` · `list_releases` | Lista refs y releases del repositorio |
| `list_commits` · `get_commit` | Navega el historial de commits y sus detalles |
| `create_issue` · `list_issues` | Abre y lista issues (filtrable por estado) |
| `comment_issue` · `close_issue` · `label_issue` | Gestiona issues existentes |
| `get_file` · `commit_file` | Lee y escribe archivos del repositorio vía la Content API |
| `get_pr` · `create_pr` · `review_pr` · `merge_pr` | Ciclo completo de pull requests |
| `search_code` · `search_issues` | Busca código e issues en todo GitHub |

</details>

<details>
<summary><b>mcp-scraper</b> — scraping ligero sobre <code>colly</code> + <code>goquery</code>, sin Chromium</summary>
<br>

| Tool | Descripción |
|------|-------------|
| `fetch_page` | Descarga el HTML crudo de una URL |
| `extract_text` | Extrae texto limpio (elimina scripts, estilos, ruido) |
| `extract_links` | Extrae todas las URLs absolutas de una página |
| `extract_table` | Extrae tablas HTML como JSON estructurado |
| `extract_metadata` | Extrae título, descripción, canonical, `og:*`, `twitter:*` |
| `search_in_page` | Busca texto o regex en el texto visible, con contexto |

</details>

<details>
<summary><b>mcp-termux</b> — interacción directa con el entorno Termux</summary>
<br>

| Tool | Descripción |
|------|-------------|
| `run_command` | Ejecuta un comando de shell (restringible por allowlist) |
| `install_pkg` · `list_pkgs` | Gestión de paquetes vía `pkg` |
| `read_env` | Lee una o todas las variables de entorno |
| `get_storage` | Uso de almacenamiento del home, el prefix y el compartido |
| `termux_battery_status` · `termux_location` | Estado del dispositivo vía Termux:API |
| `termux_notification` · `termux_toast` | Muestra notificaciones y toasts |
| `termux_sms_send` · `termux_tts_speak` | Envía SMS y lee texto en voz alta vía TTS |

</details>

<details>
<summary><b>mcp-network</b> — descubrimiento de LAN mediante sondas TCP concurrentes</summary>
<br>

| Tool | Descripción |
|------|-------------|
| `scan_network` | Escanea una subred en busca de hosts activos (autodetecta la subred) |
| `check_ports` | Escanea puertos comunes en un host específico |
| `nslookup` · `reverse_dns` | Resolución DNS directa e inversa |
| `traceroute` | Traza la ruta hasta un host (sin root, vía `tracepath`) |
| `network_info` | Gateway, servidores DNS, interfaces, subred detectada |
| `list_devices` · `get_device_info` | Inventario persistente de dispositivos de escaneos anteriores |

</details>

<details>
<summary><b>mcp-clipboard</b> — puente del portapapeles de Android (requiere Termux:API)</summary>
<br>

Requiere el paquete `termux-api` y la app Android
[Termux:API](https://wiki.termux.com/wiki/Termux:API); sin ellos, las tools
fallan con un mensaje que indica qué paso falta.

| Tool | Descripción |
|------|-------------|
| `get_clipboard` | Lee el contenido actual del portapapeles (binario vía base64) |
| `set_clipboard` | Escribe texto o bytes codificados en base64 al portapapeles |
| `clear_clipboard` | Restablece el portapapeles a un valor vacío |
| `clipboard_history` | Historial en memoria (expulsión FIFO, acotado por variables) |

</details>

<details>
<summary><b>mcp-media</b> — navegación y transformación de medios dentro de una raíz configurable</summary>
<br>

El listado y las dimensiones de imagen son Go puro; la conversión, las miniaturas
y la extracción de audio usan `ffmpeg`, y `get_metadata` se enriquece con
`exiftool` cuando está instalado.

| Tool | Descripción |
|------|-------------|
| `list_media` | Lista archivos de imagen/vídeo/audio (recursivo, filtrable por tipo) |
| `get_metadata` | Tamaño, dimensiones de imagen y EXIF/metadatos de un archivo |
| `convert_image` | Convierte el formato de una imagen y/o la redimensiona |
| `thumbnail` | Genera una miniatura desde una imagen o un frame de vídeo |
| `extract_audio` | Extrae la pista de audio de un vídeo |

</details>

<details>
<summary><b>mcp-sqlite</b> — bases de datos SQLite locales, Go puro (sin CGO)</summary>
<br>

Basado en `modernc.org/sqlite`; las bases de datos son archivos bajo
`DROIDMCP_ROOT`, y todos los valores se enlazan como parámetros — los marcadores
`?` mantienen las sentencias a salvo de inyección.

| Tool | Descripción |
|------|-------------|
| `open_db` | Abre una base de datos, creando el archivo (y directorios padre) si falta |
| `query` | Ejecuta una sentencia de lectura (SELECT/WITH/PRAGMA/…) y devuelve filas en JSON |
| `execute` | Ejecuta una sentencia de escritura (INSERT/UPDATE/DELETE/DDL); devuelve filas afectadas |
| `list_tables` | Lista tablas y vistas de usuario (excluye los objetos internos `sqlite_*`) |
| `describe_table` | Esquema de columnas de una tabla (nombre, tipo, NOT NULL, default, PK) |
| `export_csv` | Vuelca el resultado de una consulta a un archivo CSV bajo la raíz |

</details>

<details>
<summary><b>mcp-sensors</b> — sensores y estado del dispositivo, solo lectura (requiere Termux:API)</summary>
<br>

Requiere el paquete `termux-api` y la app Android Termux:API. Todas las tools son
de solo lectura; los resultados pasan el JSON de la API tal cual.
`get_brightness` lee el proveedor de ajustes de Android (Termux:API no tiene
getter de brillo) y puede no estar disponible en algunos dispositivos.

| Tool | Descripción |
|------|-------------|
| `get_battery` | Nivel de batería, estado de carga, salud, temperatura |
| `get_location` | Ubicación GPS/network/passive; `last` devuelve el último fix cacheado |
| `get_wifi_info` | Conexión WiFi actual: SSID, IP, velocidad de enlace, RSSI |
| `get_brightness` | Nivel de brillo de pantalla y modo auto-brillo |
| `get_volume` | Volumen de todos los streams de audio |
| `list_sensors` | Disponibilidad de las tools más el inventario de sensores hardware |

</details>

<details>
<summary><b>mcp-notifications</b> — notificaciones de Android y No molestar (requiere Termux:API)</summary>
<br>

Requiere el paquete `termux-api` y la app Android Termux:API. `send` y `dismiss`
tienen efectos visibles pero no tocan archivos; se recomienda ejecutar con key.
`list_notifications` necesita el permiso de Acceso a Notificaciones concedido a
Termux:API. `get_dnd_status` lee el proveedor de ajustes de Android (Termux:API
no tiene getter de No molestar) y puede no estar disponible en algunos
dispositivos.

| Tool | Descripción |
|------|-------------|
| `send_notification` | Publica una notificación con contenido, y opcional título, id, prioridad |
| `list_notifications` | Lista las notificaciones activas como JSON |
| `dismiss_notification` | Descarta una notificación por id |
| `get_dnd_status` | Estado de No molestar leído de `global zen_mode` |

</details>

## Inicio rápido

**Desde una release** — cada release incluye un binario por servidor más un
`SHA256SUMS` firmado ([detalles de verificación](docs/security.md)):

```bash
curl -LO https://github.com/kahz12/DroidMCP/releases/latest/download/droidmcp-filesystem
curl -LO https://github.com/kahz12/DroidMCP/releases/latest/download/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
chmod +x droidmcp-filesystem && mv droidmcp-filesystem "$PREFIX/bin/"
```

**Desde el código fuente** — en el dispositivo ([Termux](https://f-droid.org/en/packages/com.termux/), build de F-Droid) o con compilación cruzada:

```bash
pkg install golang git make          # requisitos previos
git clone https://github.com/kahz12/DroidMCP && cd DroidMCP
make build                           # bin/droidmcp-<servidor>, uno por servidor
make install                         # opcional: copia a $PREFIX/bin
```

**Ejecutar** — exporta los requisitos del servidor y arranca el binario; el
stream MCP se sirve en `http://localhost:<puerto>/sse`:

```bash
export DROIDMCP_API_KEY="$(openssl rand -base64 32)"
export DROIDMCP_ROOT="$HOME/workspace"     # nunca lo dejes en "/"

DROIDMCP_PORT=3000 droidmcp-filesystem
```

```bash
curl -fsS http://localhost:3000/healthz
# {"status":"ok","server":"mcp-filesystem","version":"v0.2.0"}
```

## Configuración

Cada ajuste es una variable de entorno con el prefijo `DROIDMCP_`. Compartidas
por todos los servidores:

| Variable | Descripción | Por defecto |
|----------|-------------|-------------|
| `DROIDMCP_PORT` | Puerto TCP donde escucha el listener SSE | `3000` |
| `DROIDMCP_ROOT` | Raíz de archivos, validada al arrancar — obligatoria en `filesystem`, `media`, `sqlite` | — |
| `DROIDMCP_API_KEY` | Key global; si está definida, toda petición debe llevar `X-DroidMCP-Key` | sin definir |
| `DROIDMCP_<SERVER>_KEY` | Key por servidor, p. ej. `DROIDMCP_TERMUX_KEY`; prevalece sobre la global | sin definir |
| `DROIDMCP_TLS_CERT` · `DROIDMCP_TLS_KEY` | Cert y clave PEM; ambas definidas habilitan HTTPS + HSTS | sin definir |
| `DROIDMCP_LOG_LEVEL` · `DROIDMCP_LOG_FORMAT` | `debug`–`error` · `json` o texto | `info` · texto |

Las variables por servidor — tokens de GitHub, la allowlist de `run_command`,
los opt-ins de SSRF y objetivos de escaneo, los límites de historial, las rutas
de `ffmpeg`/`exiftool` — están documentadas en la
[guía de uso](docs/usage.es.md#referencia-de-configuración).

`GET /healthz` siempre responde sin autenticación para que los supervisores
puedan sondear la disponibilidad; el resto de rutas exige la key cuando hay una
configurada, comparada en tiempo constante.

## Integración con clientes

**Claude Code** — `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "sse",
      "url": "http://localhost:3000/sse",
      "headers": { "X-DroidMCP-Key": "<tu-key>" }
    }
  }
}
```

**Gemini CLI** — mismo endpoint y cabecera, con `uri` en lugar de `url`. Cambia
a `https://…` cuando TLS esté configurado.

## Seguridad

El modelo de amenazas completo y el checklist de producción están en
[`docs/security.md`](docs/security.md). La versión corta:

- **Sin modo dev en los servidores sensibles.** `filesystem`, `termux`, `media`
  y `sqlite` se niegan a arrancar sin una key explícita — y `filesystem`,
  `media` y `sqlite` exigen además `DROIDMCP_ROOT`, así un servidor sin
  configurar nunca actúa sobre `/` ni corre sin autenticación.
- **Loopback por diseño.** Todo listener se enlaza a `127.0.0.1`; exponerlo más
  allá del dispositivo es una decisión explícita que exige key y TLS.
- **Rutas en sandbox.** Se rechazan rutas absolutas y `..`, se resuelven
  symlinks y se re-verifica el confinamiento en cada ruta que tocan los
  servidores; `mcp-sqlite` además enlaza todos los valores SQL como parámetros.
- **Valores de red conservadores.** `mcp-scraper` bloquea objetivos
  privados/loopback (SSRF) y `mcp-network` bloquea los públicos; ambos son
  opt-ins explícitos.
- **`mcp-termux` es una shell remota.** Restríngelo con
  `DROIDMCP_TERMUX_ALLOWLIST`, dale una key dedicada y arráncalo solo cuando lo
  necesites.
- **Logs redactados, releases firmadas.** Los atributos con pinta de secreto se
  reemplazan por `[REDACTED]`; las releases incluyen `SHA256SUMS` con firma de
  cosign.

## Desarrollo

```
cmd/<servidor>/     un paquete main por servidor (filesystem, github, scraper,
                    termux, network, clipboard, media, sqlite, sensors,
                    notifications)
internal/           core — servidor HTTP/SSE compartido · config · logger · buildinfo
docs/               guía de uso (EN/ES) · seguridad · puesta a punto de Termux
scripts/            compilación cruzada ARM64 reproducible
```

Construido con Go 1.26 sobre [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go),
[google/go-github](https://github.com/google/go-github),
[gocolly/colly](https://github.com/gocolly/colly) +
[goquery](https://github.com/PuerkitoBio/goquery),
[modernc.org/sqlite](https://gitlab.com/cznic/sqlite) y
[spf13/viper](https://github.com/spf13/viper). El CI aplica `gofmt`, `go vet`,
`go test -race`, `golangci-lint` y `gosec`; las releases etiquetadas se
compilan de forma reproducible y se firman.

Las contribuciones son bienvenidas — lee [CONTRIBUTING.md](CONTRIBUTING.md) y
consulta [ROADMAP.md](ROADMAP.md) para ver el trabajo planificado.

## Licencia

Publicado bajo la [Licencia MIT](LICENSE).

<div align="center">
<br>

Hecho en Android, para Android — por Ale.

</div>
