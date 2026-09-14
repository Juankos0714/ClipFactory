# ============================================================
# ClipFactory — Dockerfile
# ============================================================
# Target dev: para desarrollo local con código montado vía docker-compose
# Target prod: para construir el binario final listo para copiar a nico-server

# -----------------------------------------------
# Stage: deps — descarga dependencias de Go
# -----------------------------------------------
FROM golang:1.24-bookworm AS deps

WORKDIR /app

# copiar solo go.mod y go.sum para aprovechar caché de capas
COPY go.mod go.sum ./
RUN go mod download

# -----------------------------------------------
# Stage: dev — entorno de desarrollo con herramientas
# -----------------------------------------------
FROM golang:1.24-bookworm AS dev

# instalar herramientas auxiliares para testing del pipeline
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    mediainfo \
    sqlite3 \
    curl \
    jq \
    unzip \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# TwitchDownloaderCLI: descarga de clips de Twitch (requiere .NET runtime)
# docs: https://github.com/lay295/TwitchDownloader
# ARG para fijar/actualizar la versión: docker build --build-arg TDL_VERSION=1.56.5 .
ARG TDL_VERSION=1.56.5
RUN apt-get update && apt-get install -y --no-install-recommends unzip \
    && curl -fsSL https://dot.net/v1/dotnet-install.sh -o /tmp/dotnet-install.sh \
    && chmod +x /tmp/dotnet-install.sh \
    && /tmp/dotnet-install.sh --channel 8.0 --runtime dotnet --install-dir /usr/share/dotnet \
    && ln -s /usr/share/dotnet/dotnet /usr/local/bin/dotnet \
    && rm /tmp/dotnet-install.sh \
    && curl -fsSL "https://github.com/lay295/TwitchDownloader/releases/download/${TDL_VERSION}/TwitchDownloaderCLI-${TDL_VERSION}-Linux-x64.zip" -o /tmp/tdl.zip \
    && unzip -o /tmp/tdl.zip -d /opt/twitchdownloader \
    && rm /tmp/tdl.zip \
    # el zip trae el binario en la raíz o en una subcarpeta versionada: buscarlo
    && BIN=$(find /opt/twitchdownloader -name TwitchDownloaderCLI -type f | head -1) \
    && chmod +x "$BIN" \
    && ln -s "$BIN" /usr/local/bin/TwitchDownloaderCLI \
    # chequeo informativo: el CLI puede salir != 0 en --version sin estar roto
    # (la validación real se hace al correr el contenedor)
    && (TwitchDownloaderCLI --version || echo "aviso: --version salió != 0") \
    && rm -rf /var/lib/apt/lists/*

ENV DOTNET_ROOT=/usr/share/dotnet

# crear estructura de carpetas de ClipFactory (espejo a /opt/clipfactory)
RUN mkdir -p /opt/clipfactory/{app,config,credentials,data/{incoming,processing,completed,failed,thumbnails},database,logs}

# variables de entorno
ENV CLIPFACTORY_DATA_DIR=/opt/clipfactory/data \
    CLIPFACTORY_DB_PATH=/opt/clipfactory/database/clipfactory.db \
    CLIPFACTORY_LOG_DIR=/opt/clipfactory/logs \
    CLIPFACTORY_CONFIG_DIR=/opt/clipfactory/config \
    CLIPFACTORY_CREDENTIALS_DIR=/opt/clipfactory/credentials \
    CLIPFACTORY_LOG_LEVEL=debug

WORKDIR /opt/clipfactory/app

# copiar dependencias descargadas desde stage deps
COPY --from=deps /app/go.mod /app/go.sum ./
RUN go mod download

# copiar código fuente (se sobrescribe por volumen en docker-compose)
COPY . .

# compilar la app en /opt/clipfactory/bin/clipfactory (fuera del volumen montado por docker-compose)
RUN CGO_ENABLED=0 go build -o /opt/clipfactory/bin/clipfactory ./cmd/clipfactory

# hacer el binario ejecutable
RUN chmod +x /opt/clipfactory/bin/clipfactory

ENTRYPOINT ["/opt/clipfactory/bin/clipfactory"]
CMD ["--help"]

# -----------------------------------------------
# Stage: prod — binario final minimal
# -----------------------------------------------
FROM golang:1.24-bookworm AS prod

# instalar solo las herramientas necesarias para ejecutar (ffmpeg, sqlite3 para debugging)
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    mediainfo \
    sqlite3 \
    unzip \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# TwitchDownloaderCLI + runtime .NET (mismo setup que el stage dev)
ARG TDL_VERSION=1.56.5
RUN curl -fsSL https://dot.net/v1/dotnet-install.sh -o /tmp/dotnet-install.sh \
    && chmod +x /tmp/dotnet-install.sh \
    && /tmp/dotnet-install.sh --channel 8.0 --runtime dotnet --install-dir /usr/share/dotnet \
    && ln -s /usr/share/dotnet/dotnet /usr/local/bin/dotnet \
    && rm /tmp/dotnet-install.sh \
    && curl -fsSL "https://github.com/lay295/TwitchDownloader/releases/download/${TDL_VERSION}/TwitchDownloaderCLI-${TDL_VERSION}-Linux-x64.zip" -o /tmp/tdl.zip \
    && unzip -o /tmp/tdl.zip -d /opt/twitchdownloader \
    && rm /tmp/tdl.zip \
    && BIN=$(find /opt/twitchdownloader -name TwitchDownloaderCLI -type f | head -1) \
    && chmod +x "$BIN" \
    && ln -s "$BIN" /usr/local/bin/TwitchDownloaderCLI \
    && (TwitchDownloaderCLI --version || echo "aviso: --version salió != 0")

ENV DOTNET_ROOT=/usr/share/dotnet

# crear estructura de carpetas
RUN mkdir -p /opt/clipfactory/{app,config,credentials,data/{incoming,processing,completed,failed,thumbnails},database,logs}

ENV CLIPFACTORY_DATA_DIR=/opt/clipfactory/data \
    CLIPFACTORY_DB_PATH=/opt/clipfactory/database/clipfactory.db \
    CLIPFACTORY_LOG_DIR=/opt/clipfactory/logs \
    CLIPFACTORY_CONFIG_DIR=/opt/clipfactory/config \
    CLIPFACTORY_CREDENTIALS_DIR=/opt/clipfactory/credentials

WORKDIR /opt/clipfactory

# copiar dependencias y código desde stage deps
COPY --from=deps /app/go.mod /app/go.sum ./
COPY --from=deps /app/. ./

# compilar la app en /opt/clipfactory/bin/clipfactory (fuera del volumen montado por docker-compose)
RUN CGO_ENABLED=0 go build -o /opt/clipfactory/bin/clipfactory ./cmd/clipfactory

# hacer el binario ejecutable
RUN chmod +x /opt/clipfactory/bin/clipfactory

# intentar compilar de nuevo desde el código montado (esto es lo que se usará en prod real)
# la línea anterior es solo para la imagen de prueba; en prod real se copiará el binario compilado

# -----------------------------------------------
# NOTA: Este Dockerfile es para desarrollo. Para producción,
# se copiará el binario compilado a nico-server manualmente.
# -----------------------------------------------
