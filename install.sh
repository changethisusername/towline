#!/bin/sh
# Towline installer — https://towline.dev/install
# Usage: curl -fsSL towline.dev/install | sh
#
# Environment variables:
#   TOWLINE_VERSION     — version to install (default: latest)
#   TOWLINE_INSTALL_DIR — install directory (default: /usr/local/bin or ~/.local/bin)

set -eu

GITHUB_REPO="changethisusername/towline"
BINARIES="towline towline-mcp"

main() {
    detect_platform
    resolve_version
    setup_install_dir
    create_tempdir

    for bin in $BINARIES; do
        download_binary "$bin"
    done

    verify_checksums
    install_binaries
    cleanup

    echo ""
    echo "Towline ${VERSION} installed successfully!"
    echo ""
    echo "  towline     → ${INSTALL_DIR}/towline"
    echo "  towline-mcp → ${INSTALL_DIR}/towline-mcp"
    echo ""

    check_path

    if [ -t 0 ]; then
        echo "Run 'towline setup' to connect to your Portainer instance."
    fi
}

detect_platform() {
    OS="$(uname -s)"
    case "$OS" in
        Linux)  OS="linux" ;;
        Darwin) OS="darwin" ;;
        *)      error "Unsupported operating system: $OS" ;;
    esac

    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)   ARCH="amd64" ;;
        aarch64|arm64)  ARCH="arm64" ;;
        *)              error "Unsupported architecture: $ARCH" ;;
    esac

    info "Detected platform: ${OS}/${ARCH}"
}

resolve_version() {
    VERSION="${TOWLINE_VERSION:-latest}"

    if [ "$VERSION" = "latest" ]; then
        info "Resolving latest version..."
        VERSION=$(download_url "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
        if [ -z "$VERSION" ]; then
            error "Failed to resolve latest version. Set TOWLINE_VERSION explicitly or check https://github.com/${GITHUB_REPO}/releases"
        fi
    fi

    info "Installing Towline ${VERSION}"
}

setup_install_dir() {
    INSTALL_DIR="${TOWLINE_INSTALL_DIR:-}"

    if [ -z "$INSTALL_DIR" ]; then
        if is_writable "/usr/local/bin"; then
            INSTALL_DIR="/usr/local/bin"
        elif is_writable "${HOME}/.local/bin"; then
            INSTALL_DIR="${HOME}/.local/bin"
        else
            # Try with sudo
            if command_exists sudo && sudo -n true 2>/dev/null; then
                INSTALL_DIR="/usr/local/bin"
                USE_SUDO=true
            else
                INSTALL_DIR="${HOME}/.local/bin"
                mkdir -p "$INSTALL_DIR"
            fi
        fi
    fi

    USE_SUDO="${USE_SUDO:-false}"

    if [ "$USE_SUDO" = "false" ] && ! is_writable "$INSTALL_DIR"; then
        if command_exists sudo; then
            info "Need sudo to write to ${INSTALL_DIR}"
            USE_SUDO=true
        else
            error "Cannot write to ${INSTALL_DIR} and sudo is not available. Set TOWLINE_INSTALL_DIR to a writable directory."
        fi
    fi

    info "Install directory: ${INSTALL_DIR}"
}

create_tempdir() {
    TMPDIR_INSTALL="$(mktemp -d)"
    trap 'rm -rf "$TMPDIR_INSTALL"' EXIT
}

download_binary() {
    bin="$1"
    archive="${bin}_${VERSION}_${OS}_${ARCH}.tar.gz"
    url="https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/${archive}"

    info "Downloading ${archive}..."
    download_file "$url" "${TMPDIR_INSTALL}/${archive}"

    info "Extracting ${bin}..."
    tar -xzf "${TMPDIR_INSTALL}/${archive}" -C "${TMPDIR_INSTALL}" "$bin" 2>/dev/null || \
        tar -xzf "${TMPDIR_INSTALL}/${archive}" -C "${TMPDIR_INSTALL}"
}

verify_checksums() {
    checksums_url="https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/checksums.txt"
    checksums_file="${TMPDIR_INSTALL}/checksums.txt"

    if download_file "$checksums_url" "$checksums_file" 2>/dev/null; then
        info "Verifying checksums..."
        cd "$TMPDIR_INSTALL"

        if command_exists sha256sum; then
            sha256sum -c checksums.txt --ignore-missing 2>/dev/null || \
                warn "Checksum verification failed — continuing anyway"
        elif command_exists shasum; then
            shasum -a 256 -c checksums.txt --ignore-missing 2>/dev/null || \
                warn "Checksum verification failed — continuing anyway"
        else
            warn "No sha256sum or shasum available — skipping checksum verification"
        fi

        cd - > /dev/null
    else
        warn "No checksums.txt found — skipping verification"
    fi
}

install_binaries() {
    for bin in $BINARIES; do
        if [ ! -f "${TMPDIR_INSTALL}/${bin}" ]; then
            error "Binary not found after extraction: ${bin}"
        fi

        chmod +x "${TMPDIR_INSTALL}/${bin}"

        if [ "$USE_SUDO" = "true" ]; then
            sudo install -m 755 "${TMPDIR_INSTALL}/${bin}" "${INSTALL_DIR}/${bin}"
        else
            install -m 755 "${TMPDIR_INSTALL}/${bin}" "${INSTALL_DIR}/${bin}"
        fi

        info "Installed ${bin} → ${INSTALL_DIR}/${bin}"
    done
}

check_path() {
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            warn "${INSTALL_DIR} is not in your PATH."
            echo ""
            echo "  Add it to your shell profile:"
            echo ""

            if [ -n "${ZSH_VERSION:-}" ] || [ "$(basename "${SHELL:-}")" = "zsh" ]; then
                echo "    echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ~/.zshrc"
                echo "    source ~/.zshrc"
            elif [ "$(basename "${SHELL:-}")" = "fish" ]; then
                echo "    fish_add_path ${INSTALL_DIR}"
            else
                echo "    echo 'export PATH=\"${INSTALL_DIR}:\$PATH\"' >> ~/.bashrc"
                echo "    source ~/.bashrc"
            fi
            echo ""
            ;;
    esac
}

cleanup() {
    # trap handles this, but be explicit
    :
}

# --- Utilities ---

download_url() {
    url="$1"
    if command_exists curl; then
        curl --proto '=https' --tlsv1.2 -fsSL "$url"
    elif command_exists wget; then
        wget -qO- "$url"
    else
        error "Neither curl nor wget found. Install one and try again."
    fi
}

download_file() {
    url="$1"
    dest="$2"
    if command_exists curl; then
        curl --proto '=https' --tlsv1.2 -fsSL -o "$dest" "$url"
    elif command_exists wget; then
        wget -q -O "$dest" "$url"
    else
        error "Neither curl nor wget found. Install one and try again."
    fi
}

is_writable() {
    dir="$1"
    [ -d "$dir" ] && [ -w "$dir" ]
}

command_exists() {
    command -v "$1" > /dev/null 2>&1
}

info() {
    echo "  → $1"
}

warn() {
    echo "  ⚠ $1" >&2
}

error() {
    echo "  ✗ $1" >&2
    exit 1
}

main "$@"
