# DroidMCP Roadmap

> MCP servers nativos para Android/Termux — binarios ARM64 sin dependencias externas.
> **Stack:** Go · HTTP/SSE · Monorepo · Target: Linux ARM64

---

## Visión General

DroidMCP es una colección de servidores MCP (Model Context Protocol) diseñados para correr
nativamente en Android a través de Termux. Sin Node.js, sin Python, sin dependencias —
solo un binario que funciona.

```
Claude Code / Gemini CLI
        │
        │ HTTP/SSE (MCP Protocol)
        ▼
  DroidMCP Server  ←── corre en Termux (Android)
        │
   ┌────┼────────────────────┐
   ▼    ▼                    ▼
 Files  GitHub            Scraper ...
```

---

## Stack Tecnológico

| Componente       | Tecnología                  |
|------------------|-----------------------------|
| Lenguaje         | Go                          |
| Transporte MCP   | HTTP/SSE                    |
| MCP SDK          | `mark3labs/mcp-go`          |
| GitHub API       | `google/go-github`          |
| Scraping         | `gocolly/colly`             |
| Config           | `spf13/viper`               |
| CLI              | `spf13/cobra`               |
| Build target     | `GOOS=linux GOARCH=arm64`   |
| Estructura       | Monorepo                    |

---

## Estructura del Repositorio

```
DroidMCP/
├── cmd/
│   ├── filesystem/
│   │   └── main.go
│   ├── github/
│   │   └── main.go
│   ├── scraper/
│   │   └── main.go
│   ├── termux/
│   │   └── main.go
│   ├── adb/
│   │   └── main.go
│   └── network/
│       └── main.go
├── internal/
│   ├── core/
│   │   └── server.go
│   ├── logger/
│   │   └── logger.go
│   └── config/
│       └── config.go
├── scripts/
│   └── build-arm64.sh
├── docs/
│   ├── setup-termux.md
│   ├── claude-code-integration.md
│   └── gemini-cli-integration.md
├── .github/
│   └── workflows/
│       └── build.yml
├── Makefile
├── go.mod
├── ROADMAP.md
└── README.md
```

---

## FASE 0 — Fundación
> **Duración estimada:** 1-2 semanas
> **Objetivo:** Repo funcional con core compartido y pipeline de build ARM64

### Setup inicial
- [ ] Crear repo `DroidMCP` en GitHub
- [ ] Instalar Go en Termux (`pkg install golang`)
- [ ] Inicializar monorepo con `go mod init github.com/kahz12/droidmcp`
- [ ] Definir convenciones de código y estructura de carpetas

### Core compartido `internal/`
- [ ] `internal/core/server.go` — MCP base server HTTP/SSE reutilizable
- [ ] `internal/logger/logger.go` — logger estructurado compartido
- [ ] `internal/config/config.go` — carga de configuración por variables de entorno

### Build pipeline
- [ ] `scripts/build-arm64.sh` — compila todos los binarios para ARM64
- [ ] `Makefile` — comandos: `build`, `test`, `clean`, `install`
- [ ] `.github/workflows/build.yml` — CI/CD: build + release automático en cada tag

### Entregable
```bash
# Al final de la Fase 0 esto debe funcionar en Termux:
./droidmcp-filesystem --port 3000
# → Server MCP corriendo, listo para conectar
```

---

## FASE 1 — mcp-filesystem
> **Duración estimada:** 1-2 semanas
> **Objetivo:** Primer MCP funcional — exponer directorios Android a Claude Code / Gemini CLI

### Herramientas MCP a implementar
| Tool              | Descripción                          |
|-------------------|--------------------------------------|
| `read_file`       | Leer contenido de un archivo         |
| `write_file`      | Escribir/crear un archivo            |
| `list_directory`  | Listar contenido de un directorio    |
| `search_files`    | Buscar archivos por nombre o patrón  |
| `delete_file`     | Eliminar un archivo                  |
| `move_file`       | Mover o renombrar un archivo         |

### Tareas
- [ ] Implementar cada tool con manejo de errores robusto
- [ ] Respetar permisos de Android (scoped storage)
- [ ] Configurar directorio raíz via variable de entorno `DROIDMCP_ROOT`
- [ ] Tests unitarios para cada tool
- [ ] Documentación: `docs/setup-termux.md`
- [ ] Guía de integración con Claude Code y Gemini CLI

### Entregable
```bash
# En Termux:
export DROIDMCP_ROOT=/sdcard/proyectos
./droidmcp-filesystem --port 3000

# En Claude Code / Gemini CLI:
# MCP conectado → acceso total al directorio de proyectos
```

---

## FASE 2 — mcp-github
> **Duración estimada:** 1-2 semanas
> **Objetivo:** Operaciones GitHub completas desde Android sin Node ni npm

### Herramientas MCP a implementar
| Tool              | Descripción                          |
|-------------------|--------------------------------------|
| `list_repos`      | Listar repositorios del usuario      |
| `get_repo`        | Info detallada de un repo            |
| `create_issue`    | Abrir un issue                       |
| `list_issues`     | Listar issues de un repo             |
| `get_pr`          | Obtener detalles de un PR            |
| `create_pr`       | Crear un Pull Request                |
| `commit_file`     | Hacer commit de un archivo           |
| `get_file`        | Leer un archivo del repo             |

### Tareas
- [ ] Auth via `GITHUB_TOKEN` (Personal Access Token)
- [ ] Integrar `google/go-github`
- [ ] Rate limiting handler
- [ ] Tests con mock de GitHub API
- [ ] Documentación y ejemplos

---

## FASE 3 — mcp-scraper
> **Duración estimada:** 1-2 semanas
> **Objetivo:** Scraping liviano sin Chromium ni Playwright — nativo ARM64

### Herramientas MCP a implementar
| Tool               | Descripción                              |
|--------------------|------------------------------------------|
| `fetch_page`       | Obtener HTML de una URL                  |
| `extract_text`     | Extraer texto limpio de una página       |
| `extract_links`    | Extraer todos los links de una página    |
| `search_in_page`   | Buscar texto o patrón en una página      |
| `extract_table`    | Extraer tablas HTML como JSON            |

### Tareas
- [ ] Integrar `gocolly/colly` + `goquery`
- [ ] User-agent configurable
- [ ] Manejo de rate limiting y timeouts
- [ ] Soporte básico de headers personalizados
- [ ] Documentación con casos de uso reales

> ⚠️ **Nota:** Este MCP cubre páginas sin JS pesado. Para SPAs/React
> se evaluará una solución con rod o chromedp en Fase 5.

---

## FASE 4 — mcp-termux
> **Duración estimada:** 1-2 semanas
> **Objetivo:** Darle manos a Claude dentro del propio Termux

### Herramientas MCP a implementar
| Tool              | Descripción                          |
|-------------------|--------------------------------------|
| `run_command`     | Ejecutar un comando en Termux        |
| `install_pkg`     | Instalar un paquete con pkg          |
| `list_pkgs`       | Listar paquetes instalados           |
| `read_env`        | Leer variables de entorno            |
| `get_storage`     | Info de almacenamiento disponible    |

### Tareas
- [ ] Sandbox de seguridad — lista blanca de comandos permitidos
- [ ] Timeout configurable por comando
- [ ] Log de todos los comandos ejecutados
- [ ] Documentación de riesgos y configuración segura

> ⚠️ **Nota de seguridad:** Este MCP expone ejecución de comandos.
> Nunca exponerlo a redes externas — solo localhost.

---

## FASE 5 — mcp-network (DroidNet Integration)
> **Duración estimada:** 2-3 semanas
> **Objetivo:** Integrar capacidades de DroidNet Sentinel como MCP

### Herramientas MCP a implementar
| Tool               | Descripción                              |
|--------------------|------------------------------------------|
| `scan_network`     | Escanear dispositivos en la red local    |
| `get_device_info`  | Info detallada de un dispositivo         |
| `list_devices`     | Listar todos los dispositivos conocidos  |
| `check_ports`      | Escanear puertos de un dispositivo       |

### Tareas
- [ ] Port de lógica core de DroidNet Sentinel a Go
- [ ] Integración con Scapy existente via subprocess (opcional)
- [ ] Requiere permisos de red en Android
- [ ] Documentación de requisitos (root/no-root)

---

## FASE 6 — Pulido y Comunidad
> **Duración estimada:** 1-2 semanas
> **Objetivo:** Proyecto listo para comunidad open source

- [ ] README completo en inglés y español
- [ ] Documentación completa en `docs/`
- [ ] Demo en video corriendo en Android real
- [ ] Publicar en `awesome-mcp-servers`
- [ ] Publicar en `awesome-termux`
- [ ] Primera release oficial con todos los binarios ARM64
- [ ] Contributing guide para nuevos colaboradores

---

## Releases y Versionado

```
v0.1.0  →  Fase 0 completa (core + build pipeline)
v0.2.0  →  mcp-filesystem funcional
v0.3.0  →  mcp-github funcional
v0.4.0  →  mcp-scraper funcional
v0.5.0  →  mcp-termux funcional
v0.6.0  →  mcp-network funcional
v1.0.0  →  Fase 6 completa — release público
```

---

## Primeros pasos en Termux

```bash
# 1. Instalar Go
pkg update && pkg install golang git

# 2. Clonar el repo
git clone https://github.com/kahz12/DroidMCP
cd DroidMCP

# 3. Inicializar módulo
go mod init github.com/kahz12/droidmcp

# 4. Instalar dependencias core
go get github.com/mark3labs/mcp-go
go get github.com/spf13/cobra
go get github.com/spf13/viper

# 5. Primer build
go build ./cmd/filesystem/...
```

---

*DroidMCP — Hecho desde Android, para Android.*
