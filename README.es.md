# DroidMCP

> 🇬🇧 [English version](README.md)

Servidores MCP (Model Context Protocol) nativos para Android/Termux. Binarios ARM64 de alto rendimiento escritos en Go, sin dependencias externas en tiempo de ejecución.

Sin Node.js. Sin Python. Solo un binario que funciona.

---

## Descripción general

DroidMCP es un monorepo de servidores MCP diseñados para ejecutarse de forma nativa en Android a través de Termux. Cada servidor expone un conjunto de tools sobre HTTP/SSE que cualquier cliente compatible con MCP (Claude Code, Gemini CLI, etc.) puede consumir directamente.

```
Claude Code / Gemini CLI / Cualquier cliente MCP
              |
              | HTTP/SSE (protocolo MCP)
              v
       Servidor DroidMCP       <-- corre en Termux (Android)
              |
    +---------+---------+----------+---------+-----------+
    |         |         |          |         |           |
 filesystem github   scraper   termux   network   clipboard
```

## Servidores

### mcp-filesystem

Operaciones de archivos seguras dentro de un directorio raíz configurable. Incluye protección contra path traversal.

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

### mcp-github

Operaciones completas de GitHub usando un Personal Access Token. Construido sobre `google/go-github`.

| Tool | Descripción |
|------|-------------|
| `list_repos` | Lista los repositorios del usuario autenticado |
| `get_repo` | Obtiene metadatos detallados de un repositorio |
| `list_branches` / `list_tags` / `list_releases` | Lista refs y releases del repositorio |
| `list_commits` / `get_commit` | Navega el historial de commits y sus detalles |
| `fork_repo` | Hace fork de un repositorio |
| `create_issue` | Abre un issue nuevo |
| `list_issues` | Lista issues (filtrable por estado) |
| `comment_issue` / `close_issue` / `label_issue` | Gestiona issues existentes |
| `get_file` | Lee un archivo de un repositorio (decodifica Base64 automáticamente) |
| `get_pr` | Obtiene detalles de un pull request |
| `create_pr` | Crea un pull request nuevo |
| `review_pr` / `merge_pr` | Revisa y mergea pull requests |
| `commit_file` | Crea o actualiza un archivo vía la Content API |
| `search_code` / `search_issues` | Busca código e issues en todo GitHub |

### mcp-scraper

Web scraping ligero sin Chromium ni Playwright. Construido sobre `colly` y `goquery`.

| Tool | Descripción |
|------|-------------|
| `fetch_page` | Descarga el HTML crudo de una URL |
| `extract_text` | Extrae texto limpio (elimina scripts, estilos, ruido) |
| `extract_links` | Extrae todas las URLs absolutas de una página |
| `extract_table` | Extrae tablas HTML como JSON estructurado |
| `extract_metadata` | Extrae título, descripción, canonical, `og:*`, `twitter:*` |
| `search_in_page` | Busca texto o regex en el texto visible de una página, con contexto |

### mcp-termux

Interacción directa con el entorno Termux. Permite a los agentes de IA ejecutar comandos y gestionar paquetes.

| Tool | Descripción |
|------|-------------|
| `run_command` | Ejecuta un comando de shell |
| `install_pkg` | Instala un paquete vía `pkg install` |
| `list_pkgs` | Lista los paquetes instalados |
| `read_env` | Lee una o todas las variables de entorno |
| `get_storage` | Uso de almacenamiento del home, el prefix y el almacenamiento compartido |
| `termux_battery_status` / `termux_location` | Estado del dispositivo vía Termux:API |
| `termux_notification` / `termux_toast` | Muestra notificaciones y toasts |
| `termux_sms_send` / `termux_tts_speak` | Envía SMS y lee texto en voz alta vía TTS |

### mcp-network

Descubrimiento de red local y escaneo de puertos mediante sondas TCP concurrentes.

| Tool | Descripción |
|------|-------------|
| `scan_network` | Escanea una subred en busca de hosts activos (autodetecta la subred local) |
| `check_ports` | Escanea puertos comunes en un host específico |
| `nslookup` / `reverse_dns` | Resolución DNS directa e inversa |
| `traceroute` | Traza la ruta hasta un host (sin root vía `tracepath`) |
| `network_info` | Gateway, servidores DNS, interfaces, subred detectada |
| `list_devices` | Lista los dispositivos recordados de escaneos anteriores (inventario persistente) |
| `get_device_info` | Detalles de un dispositivo conocido por IP o MAC |

### mcp-clipboard

Gestión del portapapeles entre Android y agentes de IA vía Termux API.

> Requiere el paquete `termux-api` (`pkg install termux-api`) **y** la
> app Android [Termux:API](https://wiki.termux.com/wiki/Termux:API).
> Sin ellos, las tools fallan con un mensaje que indica qué paso falta.

| Tool | Descripción |
|------|-------------|
| `get_clipboard` | Lee el contenido actual del portapapeles (soporta binario vía base64) |
| `set_clipboard` | Escribe texto o bytes codificados en base64 al portapapeles |
| `clear_clipboard` | Restablece el portapapeles a un valor vacío |
| `clipboard_history` | Recupera el historial en memoria (expulsión FIFO, acotado por variables de entorno) |

---

## Instalación

### Requisitos previos

- Dispositivo Android con [Termux](https://f-droid.org/en/packages/com.termux/) instalado (se recomienda F-Droid)
- Go, Git y Make disponibles en Termux

```bash
pkg update && pkg upgrade
pkg install golang git make
```

### Compilar desde el código fuente

```bash
git clone https://github.com/kahz12/DroidMCP
cd DroidMCP
make build
```

Los binarios se generan en `bin/`:

```
bin/
  droidmcp-filesystem
  droidmcp-github
  droidmcp-scraper
  droidmcp-termux
  droidmcp-network
  droidmcp-clipboard
```

### Instalar en el PATH (opcional)

```bash
make install
```

Esto copia todos los binarios al `$PREFIX/bin` de Termux, dejándolos disponibles globalmente.

### Compilación cruzada para ARM64

Si compilas desde otra máquina:

```bash
make build-arm64
```

---

## Configuración

Todos los servidores se configuran mediante variables de entorno con el prefijo `DROIDMCP_`.
La guía operativa completa (autenticación, TLS, logging, modelo de amenazas,
checklist de producción) está en [`docs/security.md`](docs/security.md). La
tabla siguiente es una referencia rápida.

### Núcleo (todos los servidores)

| Variable | Descripción | Por defecto |
|----------|-------------|-------------|
| `DROIDMCP_PORT` | Puerto TCP donde escucha el listener SSE | `3000` |
| `DROIDMCP_ROOT` | Raíz para operaciones de archivos; se valida al arrancar. **Obligatoria en `mcp-filesystem`** (se niega a arrancar sin ella). | `/` (solo la usan otros servidores, que la ignoran) |
| `DROIDMCP_API_KEY` | API key global. Si está definida, toda petición debe llevarla en `X-DroidMCP-Key` | sin definir (modo dev) |
| `DROIDMCP_<SERVER>_KEY` | Override por servidor, p. ej. `DROIDMCP_TERMUX_KEY`. Tiene prioridad sobre la key global. | sin definir |
| `DROIDMCP_TLS_CERT` | Ruta al certificado TLS (PEM). Si se define junto con `_KEY`, habilita HTTPS + HSTS. | sin definir |
| `DROIDMCP_TLS_KEY` | Ruta a la clave privada TLS (PEM). | sin definir |
| `DROIDMCP_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `DROIDMCP_LOG_FORMAT` | `json` para logs estructurados, cualquier otro valor para texto | `text` |

### Por servidor

| Variable | Usada por | Descripción |
|----------|-----------|-------------|
| `GITHUB_TOKEN` / `GITHUB_APP_TOKEN` / `GITHUB_FINE_GRAINED_TOKEN` | `mcp-github` | Obligatoria. Se usa la primera que esté definida. |
| `DROIDMCP_MAX_READ_BYTES` | `mcp-filesystem` | Límite de bytes que un `read_file` mantiene en memoria (10 MiB por defecto). Pagina archivos mayores con `offset`/`length`. |
| `DROIDMCP_TERMUX_ALLOWLIST` | `mcp-termux` | Lista blanca separada por comas para `run_command` (vacía = permitir todo). |
| `DROIDMCP_SCRAPER_ALLOW_PRIVATE` | `mcp-scraper` | Ponla a `1` para permitir URLs RFC1918/loopback (desactivado por defecto por seguridad SSRF). |
| `DROIDMCP_NETWORK_ALLOW_PUBLIC` | `mcp-network` | Ponla a `1` para permitir objetivos de escaneo fuera de RFC1918. |
| `DROIDMCP_NETWORK_DB` | `mcp-network` | Ruta al JSON del inventario persistente de dispositivos (por defecto `~/.droidmcp/network-devices.json`). |
| `DROIDMCP_CLIPBOARD_HISTORY_ENTRIES` | `mcp-clipboard` | Límite de entradas del historial en memoria. |
| `DROIDMCP_CLIPBOARD_HISTORY_BYTES` | `mcp-clipboard` | Límite de bytes del historial en memoria. |

### Salud y autenticación

- `GET /healthz` siempre devuelve `200 {"status":"ok","server":<name>,"version":<v>}` y omite la autenticación, de modo que un supervisor (systemd, Docker, k8s) pueda sondear el servidor sin conocer la key.
- El resto de rutas exige la cabecera `X-DroidMCP-Key` cuando `DROIDMCP_API_KEY` (o el override por servidor) está definida. La comparación es en tiempo constante. Sin key configurada, la mayoría de servidores registran `auth=disabled` y aceptan todas las peticiones — usa ese modo solo en `localhost`. `mcp-termux` y `mcp-filesystem` son excepciones: se niegan a arrancar sin key.

---

## Uso

Cada servidor levanta un endpoint HTTP/SSE. El stream SSE está disponible en `http://localhost:<puerto>/sse`.

### Filesystem

```bash
export DROIDMCP_PORT=3000
export DROIDMCP_ROOT=/sdcard/Documents          # obligatoria — el servidor no arranca sin ella
export DROIDMCP_FILESYSTEM_KEY="$(openssl rand -base64 32)"  # obligatoria — o DROIDMCP_API_KEY
droidmcp-filesystem
```

### GitHub

```bash
export DROIDMCP_PORT=3001
export GITHUB_TOKEN=ghp_tu_token_aqui
droidmcp-github
```

### Scraper

```bash
export DROIDMCP_PORT=3002
droidmcp-scraper
```

### Termux

```bash
export DROIDMCP_PORT=3003
droidmcp-termux
```

### Network

```bash
export DROIDMCP_PORT=3004
droidmcp-network
```

### Clipboard

```bash
export DROIDMCP_PORT=3005
droidmcp-clipboard
```

### Ejemplo de producción (auth + TLS)

```bash
# Una key compartida fuerte (o keys por servidor con DROIDMCP_<NAME>_KEY).
export DROIDMCP_API_KEY="$(openssl rand -base64 32)"

# Material TLS — obligatorio para cualquier exposición fuera de loopback.
export DROIDMCP_TLS_CERT=/etc/droidmcp/cert.pem
export DROIDMCP_TLS_KEY=/etc/droidmcp/key.pem

# Restringe filesystem a un directorio dedicado; nunca lo dejes en "/".
export DROIDMCP_ROOT=/srv/droidmcp/workspace

# Logs JSON para enviarlos a un agregador; nivel info es suficiente en régimen estable.
export DROIDMCP_LOG_FORMAT=json
export DROIDMCP_LOG_LEVEL=info

droidmcp-filesystem
```

Las sondas de salud de un supervisor no necesitan la key:

```bash
curl -fsS https://localhost:3000/healthz
# {"status":"ok","server":"mcp-filesystem","version":"dev"}
# (version es "dev" en builds locales; los binarios de release reportan el tag de git)
```

Los clientes pasan la key en `X-DroidMCP-Key`:

```bash
curl -H "X-DroidMCP-Key: $DROIDMCP_API_KEY" https://localhost:3000/sse
```

---

## Integración con clientes

### Claude Code

Añade los servidores a tu configuración MCP de Claude Code (`~/.claude/settings.json`).
Cuando `DROIDMCP_API_KEY` (o una key por servidor) esté definida, incluye la
cabecera `X-DroidMCP-Key` en la entrada:

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

Para TLS, cambia la URL a `https://…` tras configurar
`DROIDMCP_TLS_CERT` / `DROIDMCP_TLS_KEY` en el servidor.

### Gemini CLI

Añade el endpoint SSE a tu configuración de Gemini CLI, con la misma
cabecera `X-DroidMCP-Key` cuando la autenticación esté habilitada:

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

---

## Estructura del proyecto

```
DroidMCP/
├── cmd/
│   ├── filesystem/       # MCP de operaciones de archivos
│   ├── github/           # MCP de la API de GitHub
│   ├── scraper/          # MCP de web scraping
│   ├── termux/           # MCP de shell y gestión de paquetes
│   ├── network/          # MCP de escaneo de red
│   └── clipboard/        # MCP de gestión del portapapeles
├── internal/
│   ├── core/server.go    # Wrapper compartido del servidor MCP (HTTP/SSE)
│   ├── logger/logger.go  # Logging estructurado (stderr)
│   └── config/config.go  # Configuración basada en variables de entorno
├── scripts/
│   └── build-arm64.sh    # Script de compilación cruzada
├── docs/
│   ├── setup-termux.md   # Guía detallada de configuración de Termux
│   └── security.md       # Modelo de amenazas + guía operativa dev/prod
├── .github/workflows/
│   └── build.yml         # CI/CD: build + release en cada tag
├── Makefile
├── go.mod
└── go.sum
```

## Stack tecnológico

| Componente | Tecnología |
|------------|------------|
| Lenguaje | Go |
| Transporte MCP | HTTP/SSE |
| SDK MCP | [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) |
| Cliente GitHub | [google/go-github](https://github.com/google/go-github) |
| Web Scraping | [gocolly/colly](https://github.com/gocolly/colly) + [goquery](https://github.com/PuerkitoBio/goquery) |
| Configuración | [spf13/viper](https://github.com/spf13/viper) |
| Target de compilación | `GOOS=linux GOARCH=arm64` |

---

## Consideraciones de seguridad

Lee [`docs/security.md`](docs/security.md) para el modelo de amenazas completo y
el checklist de producción. Puntos clave:

- **`mcp-filesystem` exige un `DROIDMCP_ROOT` explícito y una API key.** Se
  niega a arrancar sin ambos, de modo que un servidor sin configurar nunca puede
  caer en el inseguro `/` por defecto ni correr sin autenticación.
- **Modo dev vs producción.** Sin `DROIDMCP_API_KEY` (y sin key por servidor),
  la mayoría de servidores aceptan todas las peticiones y el banner de arranque
  registra `auth=disabled`. Ese modo está pensado para una sola shell en
  `localhost`. En cualquier otro escenario, define una key aleatoria y habilita
  TLS vía `DROIDMCP_TLS_CERT` / `DROIDMCP_TLS_KEY`. (`mcp-termux` y
  `mcp-filesystem` no tienen modo dev.)
- **`mcp-termux` es una shell remota.** Restríngelo con `DROIDMCP_TERMUX_ALLOWLIST`,
  dale una `DROIDMCP_TERMUX_KEY` dedicada, y directamente no lo arranques si no
  lo necesitas.
- **`mcp-clipboard` necesita `termux-api`.** Instala la app Android y ejecuta
  `pkg install termux-api` en Termux; si no, las tools devuelven un mensaje
  claro y fallan. Ver [`docs/setup-termux.md`](docs/setup-termux.md).
- **`mcp-filesystem`** rechaza rutas absolutas y traversal con `..`, luego
  resuelve symlinks y re-verifica el confinamiento, de modo que un symlink bajo
  la raíz no pueda apuntar fuera de ella (ítem 2.2 de la auditoría cerrado). La
  comprobación no es totalmente a prueba de TOCTOU, así que evita raíces donde
  puedan escribir otros procesos no confiables.
- **`mcp-scraper` / `mcp-network`** traen valores por defecto seguros (sin
  RFC1918 / sin objetivos públicos respectivamente). Anúlalos solo si entiendes
  las implicaciones de SSRF / escaneo de red.
- **`mcp-github`** actúa dentro de lo que permita el token proporcionado. Usa
  el scope más pequeño posible.
- **Los logs se redactan.** Las claves de atributos que coinciden con `token`,
  `secret`, `password`, `api_key`, `authorization` o `key` (como palabra) se
  reemplazan por `[REDACTED]` antes de llegar al sink. La cabecera
  `X-DroidMCP-Key` nunca se registra.
- **Las releases son reproducibles y firmadas.** Cada release incluye un archivo
  `SHA256SUMS` más `.sig` y `.pem` de cosign. Verifica antes de instalar.

---

## Contribuir

Las contribuciones son bienvenidas. Lee la guía en
[CONTRIBUTING.md](CONTRIBUTING.md) y consulta [ROADMAP.md](ROADMAP.md) para ver
las funcionalidades planificadas y las fases abiertas.

1. Haz fork del repositorio
2. Crea una rama de feature
3. Envía un pull request

---

## Licencia

MIT — ver [LICENSE](LICENSE) para más detalles.

Hecho desde Android, para Android.

---

¡Desarrollado con amor por Ale!
