<div align="center">

# DroidMCP

**Servidores Model Context Protocol nativos para Android y Termux.**

Servidores MCP de un solo binario escritos en Go. Nativos ARM64, sin dependencias en tiempo de ejecución — sin Node.js, sin Python, sin intérprete que instalar.

[![Build and Release](https://github.com/kahz12/DroidMCP/actions/workflows/build.yml/badge.svg)](https://github.com/kahz12/DroidMCP/actions/workflows/build.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8.svg?logo=go&logoColor=white)](go.mod)
[![Platform](https://img.shields.io/badge/platform-Android%20%C2%B7%20Termux-3DDC84.svg?logo=android&logoColor=white)](https://termux.dev)
[![Arch](https://img.shields.io/badge/arch-ARM64-555.svg)](scripts/build-arm64.sh)

[English](README.md) · [Español](README.es.md) · [Guía de uso](docs/usage.es.md) · [Roadmap](ROADMAP.md) · [Seguridad](docs/security.md)

</div>

---

## Descripción general

DroidMCP es un monorepo de servidores MCP diseñados para ejecutarse de forma nativa en Android a través de Termux. Cada servidor expone un conjunto acotado de tools sobre HTTP/SSE que cualquier cliente compatible con MCP — Claude Code, Gemini CLI o el tuyo propio — puede consumir directamente.

```
   Claude Code / Gemini CLI / Cualquier cliente MCP
                     │
                     │  HTTP/SSE (protocolo MCP)
                     ▼
              Servidor DroidMCP          corre en Termux (Android)
                     │
   ┌────────────┬────────────┬──────────┬────────────┬────────────┬────────┐
   ▼            ▼            ▼          ▼            ▼            ▼        ▼
filesystem   github      scraper    termux      network     clipboard   media
```

## Servidores

| Servidor | Enfoque | Puerto |
|----------|---------|:---:|
| `mcp-filesystem` | Operaciones de archivos en sandbox, con protección contra path traversal | `3000` |
| `mcp-github` | Acceso completo a la API de GitHub vía Personal Access Token | `3001` |
| `mcp-scraper` | Web scraping y extracción sin Chromium | `3002` |
| `mcp-termux` | Ejecución de shell y gestión de paquetes | `3003` |
| `mcp-network` | Descubrimiento de LAN y escaneo de puertos | `3004` |
| `mcp-clipboard` | Puente del portapapeles de Android vía Termux:API | `3005` |
| `mcp-media` | Navegación de medios y transformaciones con `ffmpeg` | `3006` |

<details open>
<summary><b>mcp-filesystem</b> — operaciones de archivos seguras dentro de una raíz configurable</summary>

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

| Tool | Descripción |
|------|-------------|
| `list_repos` | Lista los repositorios del usuario autenticado |
| `get_repo` | Obtiene metadatos detallados de un repositorio |
| `list_branches` · `list_tags` · `list_releases` | Lista refs y releases del repositorio |
| `list_commits` · `get_commit` | Navega el historial de commits y sus detalles |
| `fork_repo` | Hace fork de un repositorio |
| `create_issue` · `list_issues` | Abre y lista issues (filtrable por estado) |
| `comment_issue` · `close_issue` · `label_issue` | Gestiona issues existentes |
| `get_file` | Lee un archivo de un repositorio (decodifica Base64 automáticamente) |
| `get_pr` · `create_pr` | Lee y abre pull requests |
| `review_pr` · `merge_pr` | Revisa y mergea pull requests |
| `commit_file` | Crea o actualiza un archivo vía la Content API |
| `search_code` · `search_issues` | Busca código e issues en todo GitHub |

</details>

<details>
<summary><b>mcp-scraper</b> — scraping ligero sobre <code>colly</code> + <code>goquery</code>, sin Chromium</summary>

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

| Tool | Descripción |
|------|-------------|
| `run_command` | Ejecuta un comando de shell |
| `install_pkg` | Instala un paquete vía `pkg install` |
| `list_pkgs` | Lista los paquetes instalados |
| `read_env` | Lee una o todas las variables de entorno |
| `get_storage` | Uso de almacenamiento del home, el prefix y el compartido |
| `termux_battery_status` · `termux_location` | Estado del dispositivo vía Termux:API |
| `termux_notification` · `termux_toast` | Muestra notificaciones y toasts |
| `termux_sms_send` · `termux_tts_speak` | Envía SMS y lee texto en voz alta vía TTS |

</details>

<details>
<summary><b>mcp-network</b> — descubrimiento de LAN mediante sondas TCP concurrentes</summary>

| Tool | Descripción |
|------|-------------|
| `scan_network` | Escanea una subred en busca de hosts activos (autodetecta la subred) |
| `check_ports` | Escanea puertos comunes en un host específico |
| `nslookup` · `reverse_dns` | Resolución DNS directa e inversa |
| `traceroute` | Traza la ruta hasta un host (sin root, vía `tracepath`) |
| `network_info` | Gateway, servidores DNS, interfaces, subred detectada |
| `list_devices` | Lista dispositivos de escaneos anteriores (inventario persistente) |
| `get_device_info` | Detalles de un dispositivo conocido por IP o MAC |

</details>

<details>
<summary><b>mcp-clipboard</b> — puente del portapapeles de Android (requiere Termux:API)</summary>

> Requiere el paquete `termux-api` (`pkg install termux-api`) **y** la app Android
> [Termux:API](https://wiki.termux.com/wiki/Termux:API). Sin ellos, las tools
> fallan con un mensaje que indica qué paso falta.

| Tool | Descripción |
|------|-------------|
| `get_clipboard` | Lee el contenido actual del portapapeles (binario vía base64) |
| `set_clipboard` | Escribe texto o bytes codificados en base64 al portapapeles |
| `clear_clipboard` | Restablece el portapapeles a un valor vacío |
| `clipboard_history` | Historial en memoria (expulsión FIFO, acotado por variables) |

</details>

<details>
<summary><b>mcp-media</b> — navegación y transformación de medios dentro de una raíz configurable</summary>

> La conversión, las miniaturas y la extracción de audio usan `ffmpeg`
> (`pkg install ffmpeg`); `get_metadata` se enriquece con `exiftool` cuando está
> instalado. El listado y las dimensiones de imagen no necesitan herramientas
> externas. Como `mcp-filesystem`, este servidor requiere `DROIDMCP_ROOT` y una key.

| Tool | Descripción |
|------|-------------|
| `list_media` | Lista archivos de imagen/vídeo/audio (recursivo, filtrable por tipo) |
| `get_metadata` | Tamaño, dimensiones de imagen y EXIF/metadatos de un archivo |
| `convert_image` | Convierte el formato de una imagen y/o la redimensiona |
| `thumbnail` | Genera una miniatura desde una imagen o un frame de vídeo |
| `extract_audio` | Extrae la pista de audio de un vídeo |

</details>

---

## Instalación

**Requisitos previos** — un dispositivo Android con [Termux](https://f-droid.org/en/packages/com.termux/) (se recomienda la build de F-Droid), más Go, Git y Make:

```bash
pkg update && pkg upgrade
pkg install golang git make
```

**Compilar desde el código fuente:**

```bash
git clone https://github.com/kahz12/DroidMCP
cd DroidMCP
make build            # los binarios se generan en bin/
make install          # opcional: copia a $PREFIX/bin (global)
make build-arm64      # compilación cruzada desde otra máquina
```

`make build` genera un binario por servidor en `bin/`: `droidmcp-filesystem`,
`droidmcp-github`, `droidmcp-scraper`, `droidmcp-termux`, `droidmcp-network`,
`droidmcp-clipboard`, `droidmcp-media`.

---

## Configuración

Todos los servidores leen variables de entorno con el prefijo `DROIDMCP_`. La tabla
siguiente es una referencia rápida; la guía operativa completa (autenticación, TLS,
logging, modelo de amenazas) está en [`docs/security.md`](docs/security.md).

**Núcleo — todos los servidores:**

| Variable | Descripción | Por defecto |
|----------|-------------|-------------|
| `DROIDMCP_PORT` | Puerto TCP donde escucha el listener SSE | `3000` |
| `DROIDMCP_ROOT` | Raíz de archivos, validada al arrancar. **Obligatoria en `mcp-filesystem` y `mcp-media`.** | `/` (los demás la ignoran) |
| `DROIDMCP_API_KEY` | Key global. Si está definida, toda petición debe llevar `X-DroidMCP-Key` | sin definir (modo dev) |
| `DROIDMCP_<SERVER>_KEY` | Override por servidor, p. ej. `DROIDMCP_TERMUX_KEY`; prevalece sobre la global | sin definir |
| `DROIDMCP_TLS_CERT` · `DROIDMCP_TLS_KEY` | Cert y clave PEM. Ambas definidas habilitan HTTPS + HSTS | sin definir |
| `DROIDMCP_LOG_LEVEL` | `debug` · `info` · `warn` · `error` | `info` |
| `DROIDMCP_LOG_FORMAT` | `json` para logs estructurados, cualquier otro valor para texto | `text` |

**Por servidor:**

| Variable | Usada por | Descripción |
|----------|-----------|-------------|
| `GITHUB_TOKEN` · `GITHUB_APP_TOKEN` · `GITHUB_FINE_GRAINED_TOKEN` | `mcp-github` | Obligatoria; se usa la primera definida |
| `DROIDMCP_MAX_READ_BYTES` | `mcp-filesystem` | Límite por lectura en memoria (10 MiB por defecto); pagina archivos mayores con `offset`/`length` |
| `DROIDMCP_TERMUX_ALLOWLIST` | `mcp-termux` | Lista blanca de `run_command` separada por comas (vacía = permitir todo) |
| `DROIDMCP_SCRAPER_ALLOW_PRIVATE` | `mcp-scraper` | `1` permite URLs RFC1918/loopback (desactivado por defecto, seguridad SSRF) |
| `DROIDMCP_NETWORK_ALLOW_PUBLIC` | `mcp-network` | `1` permite objetivos de escaneo fuera de RFC1918 |
| `DROIDMCP_NETWORK_DB` | `mcp-network` | Ruta del inventario persistente (por defecto `~/.droidmcp/network-devices.json`) |
| `DROIDMCP_CLIPBOARD_HISTORY_ENTRIES` · `_BYTES` | `mcp-clipboard` | Límites del historial en memoria |
| `DROIDMCP_MEDIA_FFMPEG` · `DROIDMCP_MEDIA_EXIFTOOL` | `mcp-media` | Rutas explícitas opcionales a los binarios `ffmpeg` / `exiftool` (por defecto: búsqueda en PATH) |

**Salud y autenticación** — `GET /healthz` siempre devuelve `200` y omite la
autenticación, de modo que un supervisor (systemd, Docker, k8s) pueda sondear sin
la key. El resto de rutas exige `X-DroidMCP-Key` cuando hay una key configurada; la
comparación es en tiempo constante. Sin key, la mayoría de servidores registran
`auth=disabled` y aceptan todas las peticiones — úsalo solo en `localhost`.
`mcp-filesystem`, `mcp-termux` y `mcp-media` son excepciones: se niegan a arrancar sin key.

---

## Uso

Cada servidor levanta un listener HTTP/SSE; el stream se sirve en
`http://localhost:<puerto>/sse`. Define `DROIDMCP_PORT`, exporta las variables
necesarias y ejecuta el binario:

| Servidor | Puerto | Comando | Entorno requerido |
|----------|:---:|---------|-------------------|
| filesystem | `3000` | `droidmcp-filesystem` | `DROIDMCP_ROOT` + una key |
| github | `3001` | `droidmcp-github` | `GITHUB_TOKEN` |
| scraper | `3002` | `droidmcp-scraper` | — |
| termux | `3003` | `droidmcp-termux` | una key |
| network | `3004` | `droidmcp-network` | — |
| clipboard | `3005` | `droidmcp-clipboard` | paquete + app `termux-api` |
| media | `3006` | `droidmcp-media` | `DROIDMCP_ROOT` + una key (`ffmpeg` para transformar) |

**Ejemplo de producción — filesystem con auth y TLS:**

```bash
export DROIDMCP_API_KEY="$(openssl rand -base64 32)"   # o DROIDMCP_<NAME>_KEY por servidor
export DROIDMCP_TLS_CERT=/etc/droidmcp/cert.pem        # obligatorio fuera de loopback
export DROIDMCP_TLS_KEY=/etc/droidmcp/key.pem
export DROIDMCP_ROOT=/srv/droidmcp/workspace           # nunca lo dejes en "/"
export DROIDMCP_LOG_FORMAT=json                        # logs estructurados para agregar

droidmcp-filesystem
```

Las sondas de salud no necesitan la key; los clientes la pasan en la cabecera:

```bash
curl -fsS https://localhost:3000/healthz
# {"status":"ok","server":"mcp-filesystem","version":"dev"}

curl -H "X-DroidMCP-Key: $DROIDMCP_API_KEY" https://localhost:3000/sse
```

> `version` es `dev` en builds locales; los binarios de release reportan el tag de git.

---

## Integración con clientes

**Claude Code** — añade los servidores a `~/.claude/settings.json`, incluyendo la
cabecera `X-DroidMCP-Key` cuando haya una key definida:

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "sse",
      "url": "http://localhost:3000/sse",
      "headers": { "X-DroidMCP-Key": "<pega-la-key>" }
    },
    "github": {
      "type": "sse",
      "url": "http://localhost:3001/sse",
      "headers": { "X-DroidMCP-Key": "<pega-la-key>" }
    }
  }
}
```

**Gemini CLI** — mismo endpoint y cabecera, `uri` en lugar de `url`:

```json
{
  "mcpServers": {
    "filesystem": {
      "uri": "http://localhost:3000/sse",
      "headers": { "X-DroidMCP-Key": "<pega-la-key>" }
    }
  }
}
```

Cambia las URLs a `https://…` cuando `DROIDMCP_TLS_CERT` / `DROIDMCP_TLS_KEY` estén configuradas.

---

## Estructura del proyecto

```
DroidMCP/
├── cmd/                    # un paquete main por servidor
│   ├── filesystem/  github/  scraper/  termux/
│   └── network/  clipboard/  media/
├── internal/
│   ├── core/server.go      # wrapper compartido del servidor MCP (HTTP/SSE)
│   ├── logger/logger.go    # logging estructurado (stderr)
│   └── config/config.go    # configuración basada en variables de entorno
├── scripts/build-arm64.sh  # compilación cruzada
├── docs/                   # setup-termux.md · security.md
├── .github/workflows/      # build.yml — CI: build + release en cada tag
└── Makefile · go.mod · go.sum
```

## Stack tecnológico

| Componente | Tecnología |
|------------|------------|
| Lenguaje | Go 1.26 |
| Transporte MCP | HTTP/SSE |
| SDK MCP | [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) |
| Cliente GitHub | [google/go-github](https://github.com/google/go-github) |
| Web scraping | [gocolly/colly](https://github.com/gocolly/colly) + [goquery](https://github.com/PuerkitoBio/goquery) |
| Configuración | [spf13/viper](https://github.com/spf13/viper) |
| Target de compilación | `GOOS=linux GOARCH=arm64` |

---

## Seguridad

Modelo de amenazas y checklist de producción completos en [`docs/security.md`](docs/security.md). Puntos clave:

- **`mcp-filesystem`, `mcp-termux` y `mcp-media` no tienen modo dev.** Todos se
  niegan a arrancar sin una key; filesystem y media exigen además `DROIDMCP_ROOT`
  — un servidor sin configurar nunca cae en el inseguro `/` ni corre sin autenticación.
- **Dev vs producción.** Los demás servidores aceptan todas las peticiones cuando
  no hay key (el banner registra `auth=disabled`) — pensado solo para una sola
  shell en `localhost`. En cualquier otro escenario, define una key aleatoria y habilita TLS.
- **`mcp-termux` es una shell remota.** Restríngelo con `DROIDMCP_TERMUX_ALLOWLIST`,
  dale una key dedicada y no lo arranques si no lo necesitas.
- **Valores de red seguros por defecto.** `mcp-scraper` bloquea RFC1918/loopback y
  `mcp-network` bloquea objetivos públicos; anúlalos solo si entiendes las
  implicaciones de SSRF / escaneo.
- **Seguridad de rutas.** `mcp-filesystem` rechaza rutas absolutas y `..`, resuelve
  symlinks y re-verifica el confinamiento — y `mcp-media` aplica las mismas
  comprobaciones a cada ruta que lee o escribe. No es totalmente a prueba de TOCTOU
  — evita raíces donde puedan escribir otros procesos no confiables.
- **Los logs se redactan.** Las claves de atributos que coinciden con `token`,
  `secret`, `password`, `api_key`, `authorization` o `key` se reemplazan por
  `[REDACTED]`; la cabecera `X-DroidMCP-Key` nunca se registra.
- **Releases firmadas.** Cada release incluye `SHA256SUMS` más `.sig` y `.pem` de
  cosign. Verifica antes de instalar.

---

## Contribuir

Las contribuciones son bienvenidas. Lee [CONTRIBUTING.md](CONTRIBUTING.md) y
consulta [ROADMAP.md](ROADMAP.md) para ver el trabajo planificado. Haz fork, crea
una rama y abre un pull request.

## Licencia

Publicado bajo la [Licencia MIT](LICENSE).

<div align="center">

Hecho en Android, para Android — por Ale.

</div>
