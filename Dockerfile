# =============================================================================
# Multi-stage Dockerfile for sandbox-container
# An AI agent sandbox server providing code execution, file ops, bash, and skills APIs
# =============================================================================

# ---------------------------------------------------------------------------
# Stage 1: Build the Go binary
# ---------------------------------------------------------------------------
FROM golang:1.25-bookworm AS builder

WORKDIR /build

# Cache go module downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o sandbox-server .

# ---------------------------------------------------------------------------
# Stage 2: Final runtime image
# ---------------------------------------------------------------------------
FROM ubuntu:22.04

LABEL maintainer="Hypo"
LABEL description="AI Agent Sandbox Container - secure code execution & file management"

# Prevent interactive prompts during package installation
ENV DEBIAN_FRONTEND=noninteractive
ENV TZ=Asia/Singapore

# ---------------------------------------------------------------------------
# 1. System packages: locale, timezone, essential CLI tools
# ---------------------------------------------------------------------------
RUN apt-get update && apt-get install -y --no-install-recommends \
        # --- locale & timezone ---
        locales \
        tzdata \
        # --- build essentials ---
        build-essential \
        cmake \
        # --- version control ---
        git \
        # --- file operations ---
        curl \
        wget \
        unzip \
        zip \
        tar \
        rsync \
        # --- text editors ---
        vim \
        nano \
        # --- text processing ---
        jq \
        # --- networking ---
        net-tools \
        iproute2 \
        iputils-ping \
        netcat \
        nmap \
        # --- process monitoring ---
        htop \
        procps \
        # --- image processing ---
        imagemagick \
        # --- audio/video ---
        ffmpeg \
        # --- misc ---
        tree \
        sudo \
        bubblewrap \
        ca-certificates \
        gnupg \
        software-properties-common \
        openssh-client \
    && rm -rf /var/lib/apt/lists/*

# Generate locale
RUN locale-gen en_US.UTF-8
ENV LANG=en_US.UTF-8
ENV LANGUAGE=en_US:en
ENV LC_ALL=en_US.UTF-8

# Configure timezone
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

# ---------------------------------------------------------------------------
# 2. Python: 3.10 (system) + 3.11 + 3.12 via deadsnakes PPA
# ---------------------------------------------------------------------------
RUN add-apt-repository -y ppa:deadsnakes/ppa && \
    apt-get update && apt-get install -y --no-install-recommends \
        python3 \
        python3-pip \
        python3.11 \
        python3.11-venv \
        python3.11-dev \
        python3.12 \
        python3.12-venv \
        python3.12-dev \
        python3-venv \
    && rm -rf /var/lib/apt/lists/*

# Set python3 -> python3.10 as default
RUN update-alternatives --install /usr/bin/python3 python3 /usr/bin/python3.10 1

# ---------------------------------------------------------------------------
# 3. Node.js 22.x via NodeSource
# ---------------------------------------------------------------------------
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y --no-install-recommends nodejs && \
    rm -rf /var/lib/apt/lists/*

# ---------------------------------------------------------------------------
# 4. uv - fast Python package manager
# ---------------------------------------------------------------------------
RUN curl -LsSf https://astral.sh/uv/latest/install.sh | env CARGO_HOME=/usr/local UV_INSTALL_DIR=/usr/local/bin sh || true

# ---------------------------------------------------------------------------
# 5. Ripgrep (rg) - fast code search
# ---------------------------------------------------------------------------
RUN curl -LO https://github.com/BurntSushi/ripgrep/releases/download/13.0.0/ripgrep_13.0.0_amd64.deb \
    && dpkg -i ripgrep_13.0.0_amd64.deb \
    && rm ripgrep_13.0.0_amd64.deb

# ---------------------------------------------------------------------------
# 6. Install Python packages (comprehensive set for AI agent use cases)
# ---------------------------------------------------------------------------
RUN pip3 install --no-cache-dir \
    # --- Data Science & Numerical ---
    numpy \
    pandas \
    scipy \
    matplotlib \
    seaborn \
    plotly \
    kaleido \
    # --- Image Processing ---
    pillow \
    opencv-python-headless \
    PyMuPDF \
    pytesseract \
    # --- Web & HTTP ---
    requests \
    httpx \
    beautifulsoup4 \
    lxml \
    # --- Document Processing ---
    openpyxl \
    python-docx \
    python-pptx \
    PyPDF2 \
    xlrd \
    xlsxwriter \
    docx2txt \
    # --- CLI & Rich Output ---
    rich \
    # --- YAML/Config ---
    pyyaml \
    python-dotenv \
    # --- Web Framework ---
    fastapi \
    uvicorn \
    starlette \
    # --- Type Validation ---
    pydantic \
    # --- Async ---
    anyio \
    httpcore \
    # --- Testing ---
    pytest \
    pytest-timeout \
    # --- Database ---
    peewee \
    # --- Audio ---
    pydub \
    # --- Crypto ---
    cryptography \
    # --- Misc ---
    psutil \
    qrcode \
    tabulate \
    tqdm \
    networkx \
    pytz \
    python-dateutil \
    jinja2 \
    orjson \
    pyarrow \
    # --- Geo ---
    geopy \
    # --- Finance ---
    yfinance \
    # --- Video ---
    av \
    # --- PDF Generation ---
    weasyprint \
    # --- Charts ---
    pyecharts \
    # --- Subtitles ---
    srt \
    # --- Protobuf ---
    protobuf

# ---------------------------------------------------------------------------
# 7. Install Node.js global packages
# ---------------------------------------------------------------------------
RUN npm cache clean --force

# ---------------------------------------------------------------------------
# 8. Create non-root user (sbox) for sandbox operations
# ---------------------------------------------------------------------------
RUN groupadd -r sbox && \
    useradd -r -m -d /home/sbox -s /bin/bash -g sbox sbox && \
    echo "sbox ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

# ---------------------------------------------------------------------------
# 9. Create required directories
# ---------------------------------------------------------------------------
RUN mkdir -p /data/agents \
    && mkdir -p /data/skills \
    && mkdir -p /data/skill-registry \
    && mkdir -p /var/log/sandbox \
    && mkdir -p /home/sbox/.local/bin \
    && mkdir -p /home/sbox/.npm-global \
    && chown -R sbox:sbox /data/agents \
    && chown -R sbox:sbox /data/skills \
    && chown -R sbox:sbox /data/skill-registry \
    && chown -R sbox:sbox /var/log/sandbox \
    && chown -R sbox:sbox /home/sbox

# npm global prefix for sbox user
ENV NPM_CONFIG_PREFIX=/home/sbox/.npm-global
ENV PATH="/home/sbox/.npm-global/bin:/home/sbox/.local/bin:/usr/local/bin:${PATH}"

# ---------------------------------------------------------------------------
# 10. Copy Go binary from builder stage
# ---------------------------------------------------------------------------
COPY --from=builder /build/sandbox-server /usr/local/bin/sandbox-server
RUN chmod +x /usr/local/bin/sandbox-server

# ---------------------------------------------------------------------------
# 11. Runtime configuration
# ---------------------------------------------------------------------------
USER sbox
WORKDIR /home/sbox

EXPOSE 9090

# API key(s) for authentication (comma-separated).
# If not set, the server runs in open mode (no auth).
ENV SANDBOX_API_KEY=""

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:9090/v1/sandbox || exit 1

# Volume for persistent agent data
VOLUME ["/data/agents", "/var/log/sandbox"]

ENTRYPOINT ["sandbox-server"]
