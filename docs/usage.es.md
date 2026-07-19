# Guía de uso de DroidMCP

Una referencia completa, tool por tool, para operar los servidores DroidMCP:
cómo funciona el transporte, cada variable de entorno, cada tool con sus
parámetros y valores por defecto exactos, el JSON que devuelve cada una, y
recetas de principio a fin.

Para la configuración inicial en un dispositivo, ver
[`setup-termux.md`](setup-termux.md); para el modelo de amenazas y el checklist
de producción, ver [`security.md`](security.md). Versión en inglés:
[`usage.md`](usage.md).

## Contenido

- [Cómo funcionan los servidores](#cómo-funcionan-los-servidores)
- [Inicio rápido](#inicio-rápido)
- [Referencia de configuración](#referencia-de-configuración)
- [Hablar con un servidor](#hablar-con-un-servidor)
- [Referencia de tools](#referencia-de-tools)
  - [mcp-filesystem](#mcp-filesystem)
  - [mcp-github](#mcp-github)
  - [mcp-scraper](#mcp-scraper)
  - [mcp-termux](#mcp-termux)
  - [mcp-network](#mcp-network)
  - [mcp-clipboard](#mcp-clipboard)
  - [mcp-media](#mcp-media)
  - [mcp-sqlite](#mcp-sqlite)
- [Recetas](#recetas)
- [Resolución de problemas](#resolución-de-problemas)

---

## Cómo funcionan los servidores

Cada binario de DroidMCP es un servidor MCP que habla el Model Context Protocol
sobre HTTP con Server-Sent Events (SSE). Todos comparten el mismo núcleo, así que
el comportamiento operativo descrito aquí es idéntico en todos ellos.

**Listener solo en loopback.** El listener siempre se enlaza a
`127.0.0.1:<puerto>` — nunca a `0.0.0.0`. Por tanto, un servidor es inalcanzable
desde otros dispositivos por defecto. Para exponer uno deliberadamente debes
poner un proxy inverso (o un reenvío de puertos por SSH / `adb`) delante, y en
ese momento la autenticación y el TLS dejan de ser opcionales. Ver
[`security.md`](security.md).

**Endpoints.** Cada servidor expone tres rutas:

| Ruta | Auth | Propósito |
|------|------|-----------|
| `GET /sse` | requerida si hay key | Abre el stream SSE de larga duración. El servidor responde con un evento `endpoint` que indica al cliente a dónde enviar los mensajes. |
| `POST /message` | requerida si hay key | Transporta las llamadas JSON-RPC de una sesión establecida. El cliente aprende la URL exacta del evento `endpoint`. |
| `GET /healthz` | nunca | Sonda de salud. Siempre `200`, siempre sin autenticación. |

**Health check.** `/healthz` devuelve un pequeño documento JSON y omite la
autenticación, para que un supervisor pueda sondearlo sin conocer la key:

```json
{"status":"ok","server":"mcp-filesystem","version":"dev"}
```

`version` es `dev` en builds locales; los binarios de release reportan el tag de
git.

**Autenticación.** Cuando hay una key configurada, toda petición excepto
`/healthz` debe enviarla en la cabecera `X-DroidMCP-Key`. La comparación es en
tiempo constante. La key se resuelve por servidor: primero se comprueba
`DROIDMCP_<SERVER>_KEY` (por ejemplo `DROIDMCP_TERMUX_KEY`) y luego la global
`DROIDMCP_API_KEY`. Sin key definida, la mayoría de servidores corren en **modo
dev** — aceptan todas las peticiones y registran `auth=disabled` al arrancar.
`mcp-filesystem`, `mcp-termux`, `mcp-media` y `mcp-sqlite` no tienen modo dev: se
niegan a arrancar sin key.

**TLS.** Define `DROIDMCP_TLS_CERT` y `DROIDMCP_TLS_KEY` apuntando a archivos PEM
y el servidor sirve HTTPS y añade una cabecera HSTS. Si solo defines una de las
dos, cae a HTTP plano. Las cabeceras de respuesta `Cache-Control: no-store` y
`X-Content-Type-Options: nosniff` se envían siempre.

**Timeouts y apagado.** El timeout de lectura de cabeceras es de 10s y el de
inactividad de 120s; no hay timeout de escritura porque los streams SSE son de
larga duración. Ante `SIGINT` o `SIGTERM`, el servidor drena las conexiones en
curso durante hasta 10s antes de salir, así que `Ctrl+C` es un apagado limpio.

**Logging.** Los logs van a stderr. `DROIDMCP_LOG_LEVEL` es uno de `debug`,
`info`, `warn`, `error` (por defecto `info`); `DROIDMCP_LOG_FORMAT=json` cambia de
texto a logs estructurados. Se escribe una línea `http` por petición (diferida
hasta que el stream se cierra, en el caso de SSE). Las claves de atributos
sensibles (`token`, `secret`, `password`, `api_key`, `authorization`, `key`) se
reemplazan por `[REDACTED]`, y la cabecera `X-DroidMCP-Key` nunca se registra.

---

## Inicio rápido

Compila los binarios (instrucciones completas en
[`setup-termux.md`](setup-termux.md)):

```bash
git clone https://github.com/kahz12/DroidMCP
cd DroidMCP
make build          # los binarios se generan en bin/
```

Arranca un servidor. El de filesystem necesita raíz y key, así que es el más
laborioso; el resto siguen la misma forma:

```bash
export DROIDMCP_PORT=3000
export DROIDMCP_ROOT=/storage/emulated/0/Documents
export DROIDMCP_API_KEY="$(openssl rand -base64 32)"
./bin/droidmcp-filesystem
```

Confirma que está vivo desde una segunda shell:

```bash
curl -fsS http://localhost:3000/healthz
# {"status":"ok","server":"mcp-filesystem","version":"dev"}
```

Luego apunta un cliente MCP a `http://localhost:3000/sse` con la key en la
cabecera `X-DroidMCP-Key` (ver [Hablar con un servidor](#hablar-con-un-servidor)).

**Ejecutar varios a la vez.** Cada servidor necesita su propio puerto. En Termux,
`tmux` da a cada servidor un panel, y `Termux:Boot` puede exportar las variables
y ejecutar los binarios al arrancar el dispositivo. Una convención coherente con
el resto de la documentación:

| Servidor | Puerto sugerido | Binario |
|----------|:---:|---------|
| filesystem | `3000` | `droidmcp-filesystem` |
| github | `3001` | `droidmcp-github` |
| scraper | `3002` | `droidmcp-scraper` |
| termux | `3003` | `droidmcp-termux` |
| network | `3004` | `droidmcp-network` |
| clipboard | `3005` | `droidmcp-clipboard` |
| media | `3006` | `droidmcp-media` |
| sqlite | `3007` | `droidmcp-sqlite` |

---

## Referencia de configuración

Todo es una variable de entorno con el prefijo `DROIDMCP_`. Defínela antes de
lanzar el binario.

### Compartidas por todos los servidores

| Variable | Por defecto | Notas |
|----------|-------------|-------|
| `DROIDMCP_PORT` | `3000` | Puerto TCP del listener SSE. Debe estar en `1`–`65535` o el servidor no arranca. |
| `DROIDMCP_API_KEY` | sin definir | Key global requerida en `X-DroidMCP-Key`. Sin definir = modo dev (excepto filesystem/termux). |
| `DROIDMCP_<SERVER>_KEY` | sin definir | Override por servidor, p. ej. `DROIDMCP_GITHUB_KEY`. Prevalece sobre la global. |
| `DROIDMCP_TLS_CERT` | sin definir | Ruta al certificado PEM. Cert y key deben estar ambos definidos para habilitar HTTPS + HSTS. |
| `DROIDMCP_TLS_KEY` | sin definir | Ruta a la clave privada PEM. |
| `DROIDMCP_LOG_LEVEL` | `info` | `debug`, `info`, `warn` o `error`. |
| `DROIDMCP_LOG_FORMAT` | `text` | `json` para logs estructurados; cualquier otro valor es texto. |

### Por servidor

| Variable | Servidor | Por defecto | Notas |
|----------|----------|-------------|-------|
| `DROIDMCP_ROOT` | filesystem · media · sqlite | ninguno (requerido) | Directorio sobre el que puede actuar el servidor. Debe existir y ser un directorio. filesystem, media y sqlite no arrancan si está sin definir — el default compartido de `/` expondría todo el dispositivo. |
| `DROIDMCP_FILESYSTEM_KEY` | filesystem | sin definir | Requerida (esta o `DROIDMCP_API_KEY`); sin modo dev. |
| `DROIDMCP_MAX_READ_BYTES` | filesystem | `10485760` (10 MiB) | Límite de bytes que un solo `read_file` mantiene en memoria. Valores no numéricos o no positivos se ignoran. |
| `GITHUB_TOKEN` | github | ninguno (requerido) | Personal Access Token. Ver los tres nombres aceptados abajo. |
| `GITHUB_APP_TOKEN` | github | — | Se usa si `GITHUB_TOKEN` no está definido. |
| `GITHUB_FINE_GRAINED_TOKEN` | github | — | Se usa si los dos anteriores no están definidos. |
| `DROIDMCP_SCRAPER_ALLOW_PRIVATE` | scraper | desactivado | Ponlo a `1` para permitir objetivos loopback / RFC1918 / link-local / CGNAT. Desactivado por defecto por seguridad SSRF. |
| `DROIDMCP_TERMUX_KEY` | termux | sin definir | Requerida (esta o `DROIDMCP_API_KEY`); sin modo dev. |
| `DROIDMCP_TERMUX_ALLOWLIST` | termux | vacía (permitir todo) | Lista blanca separada por comas para `run_command`. Coincide con el comando completo o su basename. |
| `DROIDMCP_NETWORK_ALLOW_PUBLIC` | network | desactivado | Ponlo a `1` para permitir objetivos no privados en scan/`check_ports`. Desactivado por defecto. |
| `DROIDMCP_NETWORK_DB` | network | `~/.droidmcp/network-devices.json` | Ruta al JSON del inventario persistente de dispositivos. |
| `DROIDMCP_CLIPBOARD_HISTORY_ENTRIES` | clipboard | `32` | Máximo de entradas del historial. Acotado a `1`–`1024`. |
| `DROIDMCP_CLIPBOARD_HISTORY_BYTES` | clipboard | `65536` (64 KiB) | Máximo de bytes totales del historial. Acotado a `1024`–`16777216` (16 MiB). |
| `DROIDMCP_MEDIA_KEY` | media | sin definir | Requerida (esta o `DROIDMCP_API_KEY`); sin modo dev. |
| `DROIDMCP_MEDIA_FFMPEG` | media | búsqueda en PATH | Ruta explícita al binario `ffmpeg` usado por `convert_image`, `thumbnail` y `extract_audio`. |
| `DROIDMCP_MEDIA_EXIFTOOL` | media | búsqueda en PATH | Ruta explícita a `exiftool`; cuando está presente, enriquece `get_metadata`. |
| `DROIDMCP_SQLITE_KEY` | sqlite | sin definir | Requerida (esta o `DROIDMCP_API_KEY`); sin modo dev. |

El token de GitHub se resuelve en orden: `GITHUB_TOKEN`, luego
`GITHUB_APP_TOKEN`, luego `GITHUB_FINE_GRAINED_TOKEN`. Se usa el primero que esté
definido y se valida al arrancar con una llamada `GET /user`; un token inválido
falla el servidor de inmediato en lugar de fallar cada llamada a una tool más
tarde.

---

## Hablar con un servidor

DroidMCP implementa el transporte SSE de MCP, así que el camino cómodo es usar un
cliente que entienda MCP y dejar que gestione la sesión. Dos ejemplos:

**Claude Code** — añadir a `~/.claude/settings.json`:

```jsonc
{
  "mcpServers": {
    "filesystem": {
      "type": "sse",
      "url": "http://localhost:3000/sse",
      "headers": { "X-DroidMCP-Key": "<pega-la-key>" }
    }
  }
}
```

**Gemini CLI** — mismo endpoint, `uri` en lugar de `url`:

```jsonc
{
  "mcpServers": {
    "filesystem": {
      "uri": "http://localhost:3000/sse",
      "headers": { "X-DroidMCP-Key": "<pega-la-key>" }
    }
  }
}
```

Cambia el esquema a `https://` una vez que el TLS esté configurado.

**Inspección manual.** El handshake de sesión (abrir `/sse`, leer el evento
`endpoint`, enviar una petición `initialize` y luego mensajes `tools/call`) es
tedioso de conducir con `curl`. Usa el
[MCP Inspector](https://github.com/modelcontextprotocol/inspector) o cualquier
cliente MCP para pruebas interactivas. El único endpoint que vale la pena sondear
directamente con `curl` es el health check sin autenticación:

```bash
curl -fsS http://localhost:3000/healthz
```

En la referencia de tools de abajo, los argumentos son el objeto JSON que un
cliente envía como `arguments` de una petición `tools/call`. «Requerido»
significa que la llamada falla con un resultado de error si el argumento falta.

---

## Referencia de tools

Notación para cada tabla: **Requerido** marca los argumentos que deben estar
presentes; **Por defecto** es el valor usado cuando el argumento se omite. Salvo
que se indique lo contrario, las rutas de tipo string en `mcp-filesystem` son
relativas a `DROIDMCP_ROOT`.

### mcp-filesystem

Operaciones de archivos en sandbox bajo `DROIDMCP_ROOT`. Las rutas se validan en
cada llamada: se rechazan las rutas absolutas, se rechaza el traversal con `..`, y
los symlinks se resuelven y re-verifican para que un enlace dentro de la raíz no
pueda apuntar fuera de ella. Este servidor requiere tanto `DROIDMCP_ROOT` como una
key, y no tiene modo dev.

**`read_file`** — lee un archivo, opcionalmente un rango de bytes.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `path` | string | sí | — | Ruta del archivo relativa a la raíz. |
| `offset` | number | no | `0` | Byte de inicio de lectura. Debe ser no negativo. |
| `length` | number | no | `0` | Máximo de bytes a leer; `0` lee hasta el final. Debe ser no negativo y no exceder `DROIDMCP_MAX_READ_BYTES`. |

Una lectura sin límite de un archivo mayor que el tope devuelve un error que te
indica paginarlo con `offset`/`length` en lugar de truncar en silencio.

**`read_file_lines`** — lee un rango de líneas inclusivo, indexado desde 1.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `path` | string | sí | — | Ruta del archivo relativa a la raíz. |
| `start` | number | sí | — | Primera línea (indexada desde 1). Debe ser `>= 1`. |
| `end` | number | no | `0` | Última línea, inclusive. `0` significa fin de archivo; si no, debe ser `>= start`. |

**`write_file`** — escribe o crea un archivo. Los directorios padre se crean
(`0755`); el archivo se escribe `0644`, sobrescribiendo cualquier contenido
existente.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `path` | string | sí | Ruta del archivo relativa a la raíz. |
| `content` | string | sí | Contenido a escribir. |

**`list_directory`** — lista un directorio como un array JSON de entradas. Cada
entrada es `{name, type, size, mode, mode_octal, modified, uid, gid}` donde
`type` es `file`, `dir`, `symlink` u `other`, `modified` es RFC3339 UTC, y
`uid`/`gid` están presentes solo en Unix.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `path` | string | sí | Ruta del directorio relativa a la raíz. |

**`stat`** — metadatos de una sola ruta, con la misma forma que una entrada de
`list_directory`. Usa `Lstat`, así que un symlink se reporta como `symlink` en
lugar de seguirse.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `path` | string | sí | Ruta relativa a la raíz. |

**`search_files`** — búsqueda recursiva por nombre. Proporciona exactamente uno
de `pattern` o `regex`; el patrón/regex se aplica contra el nombre de cada
entrada. Devuelve rutas relativas a la raíz de búsqueda, una por línea, o
`No matches found`.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `root` | string | no | `.` | Directorio desde donde empezar (relativo a la raíz). |
| `pattern` | string | uno de | — | Glob (sintaxis `filepath.Match`). Excluyente con `regex`. |
| `regex` | string | uno de | — | Expresión regular. Excluyente con `pattern`. |
| `max_results` | number | no | `0` | Detenerse tras esta cantidad de coincidencias; `0` = sin límite. |

**`delete_file`** — elimina un archivo o directorio.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `path` | string | sí | — | Ruta relativa a la raíz. |
| `recursive` | boolean | no | `false` | Elimina directorios no vacíos recursivamente. Sin él, un directorio no vacío da error con una pista. |

**`move_file`** — mueve o renombra. Respaldado por `os.Rename`, así que origen y
destino deben estar en el mismo sistema de archivos.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `source` | string | sí | Ruta origen relativa a la raíz. |
| `destination` | string | sí | Ruta destino relativa a la raíz. |

**`copy_file`** — copia un archivo, o copia recursivamente un árbol de
directorios. Los modos de archivo se preservan; los symlinks encontrados durante
una copia de directorio se omiten.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `source` | string | sí | Ruta origen relativa a la raíz. |
| `destination` | string | sí | Ruta destino relativa a la raíz. |

### mcp-github

Operaciones completas de GitHub vía token, construidas sobre `google/go-github`.
Las llamadas de listado aceptan `per_page` (máx 100, por defecto 30) y `page`
(por defecto 1); las respuestas incluyen un bloque `_rate_limit`, y un error de
rate-limit expone la hora de reset para que un agente pueda esperar. `owner` y
`repo` son requeridos en toda tool con ámbito de repositorio.

**Repositorios**

| Tool | Args requeridos | Args opcionales |
|------|-----------------|-----------------|
| `list_repos` | — | `per_page`, `page` |
| `get_repo` | `owner`, `repo` | — |
| `list_branches` | `owner`, `repo` | `protected_only` (bool), `per_page`, `page` |
| `list_tags` | `owner`, `repo` | `per_page`, `page` |
| `list_releases` | `owner`, `repo` | `per_page`, `page` |
| `list_commits` | `owner`, `repo` | `sha` (SHA/rama de inicio), `path` (solo commits que lo tocan), `author`, `per_page`, `page` |
| `get_commit` | `owner`, `repo`, `sha` (SHA, rama o tag) | — |
| `fork_repo` | `owner`, `repo` | `organization` (destino del fork), `name` (renombrar el fork), `default_branch_only` (bool) |

**Issues**

| Tool | Args requeridos | Args opcionales |
|------|-----------------|-----------------|
| `create_issue` | `owner`, `repo`, `title` | `body` |
| `list_issues` | `owner`, `repo` | `state` (`open` por defecto, `closed`, `all`), `per_page`, `page` |
| `comment_issue` | `owner`, `repo`, `number`, `body` | — |
| `close_issue` | `owner`, `repo`, `number` | `state_reason` (`completed` por defecto, `not_planned`) |
| `label_issue` | `owner`, `repo`, `number`, `labels` (array) | `replace` (bool: reemplazar vs añadir) |

`comment_issue` funciona tanto en issues como en pull requests (un PR es un issue
en la API de GitHub).

**Pull requests**

| Tool | Args requeridos | Args opcionales |
|------|-----------------|-----------------|
| `get_pr` | `owner`, `repo`, `number` | — |
| `create_pr` | `owner`, `repo`, `title`, `head`, `base` | `body`, `draft` (bool) |
| `review_pr` | `owner`, `repo`, `number`, `event` (`APPROVE`, `REQUEST_CHANGES`, `COMMENT`) | `body` (requerido cuando `event` es `REQUEST_CHANGES`) |
| `merge_pr` | `owner`, `repo`, `number` | `commit_title`, `commit_message`, `merge_method` (`merge` por defecto, `squash`, `rebase`), `sha` (mergear solo si el head coincide) |

**Archivos**

| Tool | Args requeridos | Args opcionales |
|------|-----------------|-----------------|
| `get_file` | `owner`, `repo`, `path` | `ref` (commit/rama/tag; por defecto la rama por defecto del repo). El Base64 se decodifica automáticamente. |
| `commit_file` | `owner`, `repo`, `path`, `content`, `message` | `branch` (por defecto la rama por defecto del repo). Crea o actualiza el archivo vía la Content API. |

**Búsqueda**

| Tool | Args requeridos | Args opcionales |
|------|-----------------|-----------------|
| `search_code` | `query` | `sort` (`indexed`), `order` (`asc`/`desc`, por defecto `desc`), `per_page`, `page`. La query usa la sintaxis de búsqueda de GitHub, p. ej. `language:go addr in:file repo:owner/name`. |
| `search_issues` | `query` | `sort` (`comments`/`created`/`updated`), `order`, `per_page`, `page`. Busca issues y PRs. |

### mcp-scraper

Scraping sin Chromium sobre `colly` + `goquery`. **La protección SSRF está
activada por defecto**: los objetivos que resuelven a loopback, RFC1918, ULA
IPv6, link-local, multicast o rangos CGNAT se rechazan salvo que
`DROIDMCP_SCRAPER_ALLOW_PRIVATE=1`. Las respuestas se cachean en un LRU en memoria
con un TTL de 5 minutos; los cuerpos de respuesta se limitan a 10 MiB.

**Argumentos comunes** — aceptados por toda tool del scraper:

| Argumento | Tipo | Por defecto | Descripción |
|-----------|------|-------------|-------------|
| `headers` | object | — | Mapa de nombre de cabecera de petición a valor. |
| `user_agent` | string | — | Sobrescribe el `User-Agent` para esta petición. |
| `timeout_seconds` | number | `20` | Timeout por petición. Máx `60`. |
| `no_cache` | boolean | `false` | Evita la caché de respuestas para esta llamada. |
| `wait_selector` | string | — | Reintenta el fetch hasta que este selector CSS coincida (útil para páginas renderizadas en servidor / con carga diferida). |
| `wait_attempts` | number | `3` | Máximo de reintentos cuando `wait_selector` está definido. Máx `10`. |
| `wait_interval_ms` | number | `1000` | Retardo entre reintentos cuando `wait_selector` está definido. |

**Argumentos específicos de cada tool:**

| Tool | Args requeridos | Args opcionales extra | Devuelve |
|------|-----------------|-----------------------|----------|
| `fetch_page` | `url` (solo http/https) | — | JSON `{url, status, headers, body, ...}`. |
| `extract_text` | `url` | `selector` (por defecto `<body>`) | Texto visible limpio. |
| `extract_links` | `url` | `selector` (por defecto `a[href]`) | URLs absolutas con texto de ancla y `rel`. |
| `extract_table` | `url` | `selector` (por defecto `table`) | Tablas como JSON estructurado. |
| `extract_metadata` | `url` | — | Título, descripción, canonical, `og:*`, `twitter:*`. |
| `search_in_page` | `url`, `query` | `regex` (bool), `case_sensitive` (bool), `selector` (por defecto `<body>`), `max_results` (por defecto `20`, máx `100`), `context_chars` (por defecto `80`, máx `500`) | Coincidencias con contexto alrededor. |

Para `search_in_page`, `query` es texto literal salvo que `regex` sea `true`, en
cuyo caso es una expresión regular de Go; la coincidencia es insensible a
mayúsculas salvo que `case_sensitive` sea `true`.

### mcp-termux

Acceso directo al entorno Termux. Este servidor entrega al llamante autoridad
real sobre el dispositivo, así que **requiere una key y no tiene modo dev**. Las
tools envoltorio `termux_*` necesitan además el paquete `termux-api` y la app
Termux:API (ver [`setup-termux.md`](setup-termux.md)).

**`run_command`** — ejecuta un programa. Corre el binario directamente sin shell,
así que no hay expansión de globs/pipes/redirecciones: pasa los argumentos por
`args`, no como una sola cadena. Devuelve JSON `{stdout, stderr, exit_code, ...}`;
cada stream de salida se limita a 1 MiB.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `command` | string | sí | — | El programa a ejecutar (sin shell). |
| `args` | string[] | no | `[]` | Argumentos pasados al programa. |
| `cwd` | string | no | — | Directorio de trabajo para el proceso hijo. |
| `timeout_seconds` | number | no | `30` | Timeout por llamada. Máx `300`. |

Cuando `DROIDMCP_TERMUX_ALLOWLIST` está definida, `command` debe coincidir con una
de sus entradas separadas por comas (por valor completo o basename) o la llamada
se rechaza. Una allowlist vacía/sin definir permite cualquier comando.

**`install_pkg`** — instala un paquete vía `pkg install -y`.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `package` | string | sí | — | Nombre del paquete. |
| `timeout_seconds` | number | no | `30` | Timeout por llamada. Máx `300`. |

**`list_pkgs`** — lista los paquetes instalados. Sin argumentos.

**`read_env`** — lee variables de entorno. Devuelve `{name, value}` para una
variable nombrada, o `{vars: {...}}` cuando se omite `name`.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `name` | string | no | Variable a leer; omítela para listar todas. |

**`get_storage`** — uso de almacenamiento (bytes total/usado/disponible). Sin
`path`, reporta el home de Termux, el prefix y el almacenamiento compartido.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `path` | string | no | Inspecciona esta ruta en lugar del conjunto por defecto. |

**Envoltorios de Termux:API** (necesitan `termux-api`):

| Tool | Args requeridos | Args opcionales |
|------|-----------------|-----------------|
| `termux_battery_status` | — | `timeout_seconds` |
| `termux_location` | — | `provider` (`gps` por defecto, `network`, `passive`), `request` (`once` por defecto, `last`, `updates`), `timeout_seconds` |
| `termux_notification` | — | `title`, `content`, `id` (reutilízalo para reemplazar una notificación previa) |
| `termux_toast` | `text` | — |
| `termux_sms_send` | `number`, `text` | — (requiere permiso de SMS concedido a Termux:API) |
| `termux_tts_speak` | `text` | `language` (BCP47, p. ej. `en-US`), `rate` (`1.0` = normal), `pitch` (`1.0` = normal) |

### mcp-network

Descubrimiento de LAN mediante sondas TCP concurrentes. **Los objetivos deben
estar en un rango privado por defecto**; pon `DROIDMCP_NETWORK_ALLOW_PUBLIC=1`
para permitir hosts públicos. Los resultados de escaneo se persisten en el
inventario en `DROIDMCP_NETWORK_DB` (por defecto
`~/.droidmcp/network-devices.json`) y son lo que leen `list_devices` /
`get_device_info`.

**`scan_network`** — escanea una subred en busca de hosts activos. Devuelve JSON
por host con IP, MAC (de ARP) y puertos abiertos, y los registra en el inventario.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `subnet` | string | no | autodetectada | CIDR a escanear, p. ej. `192.168.1.0/24`. Vacío autodetecta la subred local desde la máscara de interfaz del kernel. |
| `timeout_seconds` | number | no | `30` | Timeout por llamada. Máx `120`. |

**`check_ports`** — chequeo TCP concurrente de puertos en un host. Devuelve
`{host, resolved, ports: [{port, open}]}`.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `host` | string | sí | — | IP o hostname. |
| `ports` | string | no | conjunto común | Puertos separados por comas. El conjunto por defecto es `21,22,23,25,53,80,110,135,139,143,443,445,993,995,1723,3306,3389,5900,8080`. |
| `timeout_seconds` | number | no | `15` | Timeout por llamada. Máx `60`. |

**`nslookup`** — DNS directo. Devuelve `{host, addrs}`.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `host` | string | sí | Hostname a resolver. |

**`reverse_dns`** — DNS inverso. Devuelve `{ip, names}`.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `ip` | string | sí | Dirección IP a consultar. |

**`traceroute`** — traza la ruta hasta un host. Ejecuta `traceroute` o
`tracepath`; este último no necesita root.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `host` | string | sí | — | Host objetivo. |
| `max_hops` | number | no | `30` | Máximo de saltos TTL a sondear. |
| `timeout_seconds` | number | no | `30` | Timeout por llamada. Máx `120`. |

**`network_info`** — metadatos locales: gateway por defecto (de
`/proc/net/route`), servidores DNS, interfaces y subred detectada. Sin argumentos.

**`list_devices`** — todos los dispositivos recordados de ejecuciones previas de
`scan_network`. Sin argumentos. Vacío hasta que ejecutes un escaneo.

**`get_device_info`** — detalles recordados (MAC, puertos abiertos, primera/última
vez visto) de un dispositivo.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `device` | string | sí | IP o MAC vista en un escaneo previo. |

### mcp-clipboard

Puente del portapapeles entre Android y el agente. Requiere el paquete
`termux-api` y la app Termux:API; sin ellos las tools fallan con una pista que
nombra el paso que falta. Las escrituras también se registran en un historial
acotado en el proceso (por defecto 32 entradas / 64 KiB, configurable con las dos
variables `..._HISTORY_...`).

**`get_clipboard`** — lee el portapapeles. Devuelve
`{text, bytes_len, base64, is_utf8, truncated}`; el contenido binario es
recuperable desde `base64`. Sin argumentos.

**`set_clipboard`** — escribe el portapapeles. Proporciona exactamente uno de
`text` o `text_base64`; proporcionar ambos o ninguno es un error.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `text` | string | uno de | Texto UTF-8 a escribir. |
| `text_base64` | string | uno de | Bytes codificados en base64, para contenido no UTF-8/binario. |

**`clear_clipboard`** — limpia el portapapeles del sistema y el historial en el
proceso. Devuelve `{ok, history_cleared}`. Sin argumentos.

**`clipboard_history`** — el historial de escrituras en el proceso, del más
antiguo al más reciente. Sin argumentos.

### mcp-media

Navegación y transformación de medios del dispositivo bajo `DROIDMCP_ROOT`. Las
rutas se validan igual que en `mcp-filesystem` — se rechazan las rutas absolutas y
el `..`, y los symlinks se resuelven y re-verifican para que un enlace dentro de la
raíz no pueda apuntar fuera. Como filesystem, este servidor **requiere tanto
`DROIDMCP_ROOT` como una key y no tiene modo dev**: lee y escribe archivos y lanza
subprocesos. `list_media` y las dimensiones de imagen son Go puro; las tools de
transformación usan `ffmpeg` (`pkg install ffmpeg`), y `get_metadata` se enriquece
con `exiftool` cuando está instalado. Cada `path`/`source`/`destination` es
relativo a `DROIDMCP_ROOT`.

**`list_media`** — lista archivos de medios bajo un directorio. Devuelve un array
JSON de `{name, path, type, ext, size, modified}` donde `type` es `image`, `video`
o `audio`, `path` es relativo a la raíz y `modified` es RFC3339 UTC. Los archivos
que no son medios se omiten.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `path` | string | no | `.` | Directorio a escanear, relativo a la raíz. |
| `types` | string[] | no | todos | Filtra por tipo: cualquiera de `image`, `video`, `audio`. |
| `recursive` | boolean | no | `false` | Desciende a subdirectorios. |
| `max_results` | number | no | `0` | Detenerse tras esta cantidad de coincidencias; `0` = sin límite. |

**`get_metadata`** — metadatos de un archivo de medios. Siempre devuelve
`{path, type, ext, size, modified}`; añade `width`/`height` para las imágenes cuyo
encabezado puede decodificar la stdlib (JPEG, PNG, GIF), y un objeto `exif` con el
conjunto completo de tags cuando `exiftool` está instalado (los campos con ruta
absoluta se eliminan).

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `path` | string | sí | Archivo de medios relativo a la raíz. |

**`convert_image`** — convierte una imagen y/o la redimensiona vía `ffmpeg`. El
formato de salida se toma de la extensión del destino. Devuelve JSON
`{ok, tool, source, destination, exit_code, duration_ms}`; un exit distinto de cero
expone el final del stderr de ffmpeg, y cualquier archivo de salida creado por la
ejecución fallida se elimina (`partial_output_removed: true`) — un destino que ya
existía antes de la llamada nunca se borra en la limpieza.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `source` | string | sí | — | Imagen origen relativa a la raíz. |
| `destination` | string | sí | — | Destino relativo a la raíz; la extensión elige el formato. Debe diferir de `source`. |
| `width` | number | no | `0` | Ancho objetivo en px. `0` mantiene el aspecto según `height` (o el original si ambos son `0`). |
| `height` | number | no | `0` | Alto objetivo en px. `0` mantiene el aspecto según `width`. |
| `quality` | number | no | — | Calidad `1`–`100` (mayor es mejor). Se aplica solo a destinos JPEG. |
| `timeout_seconds` | number | no | `120` | Timeout por llamada. Máx `600`. |

**`thumbnail`** — un único frame escalado de una imagen o vídeo vía `ffmpeg`. Para
vídeo, el frame se toma en `timestamp`. Misma forma de resultado que `convert_image`.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `source` | string | sí | — | Medio origen relativo a la raíz. |
| `destination` | string | sí | — | Imagen destino relativa a la raíz. Debe diferir de `source`. |
| `width` | number | no | `320` | Ancho de la miniatura en px (alto automático si se omite). |
| `height` | number | no | `0` | Alto de la miniatura en px; `0` mantiene la relación de aspecto. |
| `timestamp` | string | no | `0` | Para vídeo: posición de búsqueda, segundos (`5`) o `HH:MM:SS` (`00:00:05`). |
| `timeout_seconds` | number | no | `120` | Timeout por llamada. Máx `600`. |

**`extract_audio`** — extrae la pista de audio de un vídeo vía `ffmpeg -vn`. Por
defecto la pista se copia sin recodificar. Misma forma de resultado que
`convert_image`.

| Argumento | Tipo | Requerido | Por defecto | Descripción |
|-----------|------|:---:|-------------|-------------|
| `source` | string | sí | — | Vídeo origen relativo a la raíz. |
| `destination` | string | sí | — | Audio destino relativo a la raíz. Debe diferir de `source`. |
| `codec` | string | no | `copy` | Codec de audio, p. ej. `mp3`, `aac`, `flac`. `copy` re-multiplexa sin recodificar. |
| `bitrate` | string | no | — | Bitrate objetivo al recodificar, p. ej. `192k`. Se ignora cuando `codec` es `copy`. |
| `timeout_seconds` | number | no | `120` | Timeout por llamada. Máx `600`. |

Las tools de transformación fallan con una pista de instalación cuando no se
encuentra `ffmpeg`. Define `DROIDMCP_MEDIA_FFMPEG` / `DROIDMCP_MEDIA_EXIFTOOL` para
fijar un binario concreto.

---

### mcp-sqlite

Bases de datos SQLite locales almacenadas como archivos bajo `DROIDMCP_ROOT`. El
motor es `modernc.org/sqlite`, una implementación en Go puro, así que el binario no
necesita CGO ni `libsqlite3`. Las rutas se validan igual que en `mcp-filesystem` —
se rechazan rutas absolutas y `..`, y los symlinks se resuelven y re-verifican.
Como filesystem, este servidor **requiere `DROIDMCP_ROOT` y una key y no tiene modo
dev**: crea archivos y ejecuta SQL arbitrario.

Cada `db` y `destination` es relativo a `DROIDMCP_ROOT`. Todos los valores del
usuario deben pasarse por el array `args` en lugar de formatearse dentro del texto
SQL: se enlazan como parámetros, que es lo que mantiene las sentencias a salvo de
inyección. Usa marcadores `?` en `sql`, uno por cada elemento de `args`, en orden.
Se aceptan números, cadenas, booleanos y `null`; un número entero se enlaza como
entero.

Solo `open_db` crea una base de datos — `query`, `execute`, `list_tables`,
`describe_table` y `export_csv` devuelven un error para una ruta que aún no existe,
así un error de tipeo nunca crea en silencio una base vacía. Las conexiones se
agrupan y reutilizan entre llamadas dentro de un servidor en marcha, con un único
escritor para que las llamadas concurrentes no compitan por el archivo.

**`open_db`** — abre una base de datos, creando el archivo (y los directorios
padre que falten) si no existe. Devuelve `{path, created, sqlite_version}`, donde
`created` es `true` solo cuando esta llamada creó el archivo. Llamarla primero es
opcional; las demás tools abren una base existente de forma perezosa.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `db` | string | sí | Ruta del archivo de base de datos relativa a la raíz, p. ej. `data/app.db`. |

**`query`** — ejecuta una sentencia de solo lectura y devuelve sus filas. La
palabra clave inicial debe ser `SELECT`, `WITH`, `PRAGMA`, `EXPLAIN` o `VALUES`;
una sentencia de escritura se rechaza (usa `execute`). Devuelve
`{columns, rows, count, truncated}`, donde `rows` es un array JSON de objetos por
columna y `truncated` es `true` cuando existían más filas de las que permitía
`max_rows`. Los valores TEXT/BLOB se devuelven como cadenas.

| Argumento | Tipo | Requerido | Default | Descripción |
|-----------|------|:---:|---------|-------------|
| `db` | string | sí | — | Ruta de la base de datos relativa a la raíz. Debe existir. |
| `sql` | string | sí | — | La sentencia; usa marcadores `?` para los valores. |
| `args` | any[] | no | ninguno | Parámetros posicionales enlazados a los marcadores `?`, en orden. |
| `max_rows` | number | no | `1000` | Tope de filas devueltas; `0` es ilimitado. |
| `timeout_seconds` | number | no | `30` | Timeout por llamada. Máx `600`. |

**`execute`** — ejecuta una sentencia de escritura (`INSERT`/`UPDATE`/`DELETE`, DDL
como `CREATE`/`DROP`/`ALTER`, etc.). Devuelve `{rows_affected, last_insert_id}`
según los reporte el driver.

| Argumento | Tipo | Requerido | Default | Descripción |
|-----------|------|:---:|---------|-------------|
| `db` | string | sí | — | Ruta de la base de datos relativa a la raíz. Debe existir. |
| `sql` | string | sí | — | La sentencia; usa marcadores `?` para los valores. |
| `args` | any[] | no | ninguno | Parámetros posicionales enlazados a los marcadores `?`, en orden. |
| `timeout_seconds` | number | no | `30` | Timeout por llamada. Máx `600`. |

**`list_tables`** — lista las tablas y vistas de usuario de una base de datos. Los
objetos internos `sqlite_*` se excluyen. Devuelve un array JSON de `{name, type}`
donde `type` es `table` o `view`, ordenado por nombre.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `db` | string | sí | Ruta de la base de datos relativa a la raíz. Debe existir. |

**`describe_table`** — describe las columnas de una tabla vía `PRAGMA table_info`.
El nombre de la tabla se valida contra el esquema antes de usarlo, así no puede ser
un vector de inyección. Devuelve `{table, columns}` donde cada columna es
`{cid, name, type, notnull, default, pk}`.

| Argumento | Tipo | Requerido | Descripción |
|-----------|------|:---:|-------------|
| `db` | string | sí | Ruta de la base de datos relativa a la raíz. Debe existir. |
| `table` | string | sí | Nombre de la tabla o vista a describir. |

**`export_csv`** — ejecuta una sentencia de lectura y vuelca sus resultados en un
archivo CSV bajo la raíz (una fila de cabecera más una fila por registro).
Devuelve `{path, rows, columns}`. Los directorios padre del destino se crean; un
fallo a mitad de escritura elimina solo un archivo creado por esta llamada, nunca
datos preexistentes, y el destino debe diferir de la base de datos origen.

| Argumento | Tipo | Requerido | Default | Descripción |
|-----------|------|:---:|---------|-------------|
| `db` | string | sí | — | Ruta de la base de datos relativa a la raíz. Debe existir. |
| `sql` | string | sí | — | El `SELECT` cuyas filas se exportan; usa marcadores `?` para los valores. |
| `destination` | string | sí | — | Ruta CSV destino relativa a la raíz. Debe diferir de `db`. |
| `args` | any[] | no | ninguno | Parámetros posicionales enlazados a los marcadores `?`, en orden. |
| `timeout_seconds` | number | no | `30` | Timeout por llamada. Máx `600`. |

---

## Recetas

**Leer un log grande por páginas.** `read_file` se niega a bufferizar de una vez
un archivo mayor que `DROIDMCP_MAX_READ_BYTES`. Paginado: llama con `offset: 0,
length: 1000000`, luego `offset: 1000000`, y así sucesivamente; o usa
`read_file_lines` con una ventana `start`/`end` que avanza cuando quieres límites
de línea.

**Buscar y luego actuar.** Usa `search_files` con un `pattern` como `*.md` (o un
`regex`) para localizar archivos, y alimenta las rutas relativas devueltas
directamente a `read_file`, `move_file` o `delete_file`.

**Scrapear una tabla poblada por JavaScript.** Si `extract_table` no devuelve
nada porque la tabla se inyecta tras la carga, añade
`wait_selector: "table tbody tr"` para que el fetch reintente hasta que existan
filas, y vuelve a ejecutar la extracción.

**Abrir un PR de principio a fin.** `commit_file` el cambio en una rama,
`create_pr` desde ese `head` hacia `base`, opcionalmente `review_pr` con
`event: "APPROVE"`, y luego `merge_pr` con `merge_method: "squash"`.

**Inventariar la LAN.** Ejecuta `scan_network` una vez (autodetecta la subred)
para poblar el store, luego `list_devices` para ver todo y `get_device_info` con
una IP o MAC para un host concreto. `check_ports` profundiza en los puertos de un
host.

**Enviar una notificación desde un agente.** Con `mcp-termux` en marcha y
Termux:API instalado, `termux_notification` (title/content) o `termux_toast`
(text) muestran mensajes en el dispositivo; `termux_tts_speak` lee texto en voz
alta.

**Miniaturizar una carpeta de medios.** `list_media` con `recursive: true`
(opcionalmente `types: ["video"]`) enumera los archivos; alimenta cada `path`
devuelto a `thumbnail`, escribiendo a un destino `thumbs/` y pasando un
`timestamp` para tomar un frame representativo de los vídeos. `get_metadata` te da
dimensiones y EXIF de cualquier archivo, y `convert_image` / `extract_audio`
manejan los cambios de formato.

**Guardar estado local en SQLite.** `open_db` de un archivo bajo la raíz,
`execute` tu `CREATE TABLE`, luego `execute` los inserts con marcadores `?` y un
array `args` (nunca formatees los valores dentro del SQL). Léelo de vuelta con
`query`, inspecciona el esquema con `list_tables` / `describe_table`, y entrega una
instantánea a otra tool con `export_csv`.

---

## Resolución de problemas

| Síntoma | Causa y solución |
|---------|------------------|
| El servidor sale de inmediato, registra `requires DROIDMCP_ROOT` | `mcp-filesystem`, `mcp-media` o `mcp-sqlite` se arrancó sin `DROIDMCP_ROOT`. Defínelo a un directorio real. |
| El servidor sale, registra `requires DROIDMCP_..._KEY or DROIDMCP_API_KEY` | `mcp-filesystem`/`mcp-termux`/`mcp-media`/`mcp-sqlite` necesitan una key. Define una; no tienen modo dev. |
| El servidor sale, registra `DROIDMCP_PORT out of range` o `not a directory` | Falló la validación de configuración. El puerto debe estar en `1`–`65535`; `DROIDMCP_ROOT` debe existir y ser un directorio. |
| Los clientes reciben `401 unauthorized` | Hay una key configurada pero el cliente no envía `X-DroidMCP-Key`, o no coincide. `/healthz` está exento, así que un health check que funciona con llamadas a tools que fallan apunta a la cabecera. |
| `mcp-github` no arranca, `token validation failed` | El token falta, expiró o carece de scope. Define `GITHUB_TOKEN` (o `GITHUB_APP_TOKEN` / `GITHUB_FINE_GRAINED_TOKEN`). |
| El scraper devuelve `target is not allowed` / error de rango privado | La protección SSRF bloqueó una URL privada/loopback. Pon `DROIDMCP_SCRAPER_ALLOW_PRIVATE=1` solo si confías en el despliegue. |
| Una tool de network devuelve `not in a private network range` | El objetivo es público y `DROIDMCP_NETWORK_ALLOW_PUBLIC` está desactivado. Ponlo a `1` para permitir objetivos públicos. |
| `read_file` da error `file exceeds max read size` | El archivo es mayor que `DROIDMCP_MAX_READ_BYTES`. Paginado con `offset`/`length`, o sube el tope. |
| Los envoltorios de clipboard/termux fallan con una pista «termux-api not installed» | Instala `pkg install termux-api` y la app Termux:API, y concede sus permisos. |
| `run_command` dice que un comando `not in DROIDMCP_TERMUX_ALLOWLIST` | La allowlist está definida y no incluye ese comando. Añádelo, o vacía la variable para permitir todo. |
| Una transformación de `mcp-media` falla con una pista `ffmpeg not found` | `convert_image`/`thumbnail`/`extract_audio` necesitan ffmpeg. Ejecuta `pkg install ffmpeg`, o define `DROIDMCP_MEDIA_FFMPEG` con su ruta. |
| `mcp-sqlite` devuelve `database … does not exist; call open_db first` | Solo `open_db` crea una base de datos; `query`/`execute`/etc. requieren un archivo existente. Llama a `open_db` (o corrige la ruta). |
| `mcp-sqlite` `query` devuelve `query only runs read statements` | Se envió una sentencia de escritura a `query`. Usa `execute` para `INSERT`/`UPDATE`/`DELETE`/DDL. |
| No puedes alcanzar un servidor desde otra máquina | Es por diseño: el listener está enlazado a `127.0.0.1`. Ponle delante un proxy inverso o un reenvío de puertos, y lee [`security.md`](security.md) antes de exponerlo. |

Para cualquier cosa relacionada con seguridad — exposición, keys, TLS, el modelo
de amenazas completo — ver [`security.md`](security.md).
