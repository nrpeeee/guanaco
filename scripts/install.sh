#!/bin/bash

set -euo pipefail

# Always build from the repository root, wherever this script is run from.
cd "$(dirname "${BASH_SOURCE[0]}")/.."

readonly RED=$'\033[0;31m'
readonly GREEN=$'\033[0;32m'
readonly BLUE=$'\033[0;34m'
readonly CYAN=$'\033[0;36m'
readonly RST=$'\033[0m'

info()    { echo -e "${BLUE}[INFO]${RST} $1"; }
ok()      { echo -e "${GREEN}[SUCCESS]${RST} $1"; }
err()     { echo -e "${RED}[ERROR]${RST} $1"; }
die()     { err "$1" >&2; exit 1; }

section() {
    echo -e "\n${CYAN}════════════════════════════════════════${RST}"
    echo -e "${CYAN}  $1${RST}"
    echo -e "${CYAN}════════════════════════════════════════${RST}"
}

spinner() {
    local pid=$1
    local msg="${2:-Working...}"
    local frames='⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏'
    local i=0
    local start=$SECONDS

    while kill -0 "$pid" 2>/dev/null; do
        local elapsed=$(( SECONDS - start ))
        local h=$(( elapsed / 3600 ))
        local m=$(( (elapsed % 3600) / 60 ))
        local s=$(( elapsed % 60 ))
        printf "\r${CYAN}%s${RST} %s ${BLUE}[%02d:%02d:%02d elapsed]${RST}" \
            "${frames:$((i % ${#frames})):1}" "$msg" "$h" "$m" "$s"
        i=$(( i + 3 ))
        sleep 0.08
    done

    printf "\r\033[K"
}

run_spin() {
    local msg="$1"; shift

    if [[ -t 1 ]]; then
        local tmp
        tmp=$(mktemp)
        "$@" >"$tmp" 2>&1 &
        local pid=$!
        spinner "$pid" "$msg"
        local st
        wait "$pid" && st=0 || st=$?
        if (( st != 0 )); then
            tail -n 20 "$tmp" >&2
        fi
        rm -f "$tmp"
        return $st
    else
        "$@"
    fi
}

BINARY="Guanaco"
INSTALL_DIR="$HOME/.local/bin"
DESKTOP_DIR="$HOME/.local/share/applications"
ICON_DIR="$HOME/.local/share/icons/hicolor/256x256/apps"

section "Building & Installing $BINARY"

command -v go >/dev/null 2>&1 || die "go: command not found in PATH (install Go 1.26 or newer)"

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
VERSION="${VERSION#v}"

if run_spin "Compiling binary (version ${VERSION})" go build -ldflags="-s -w -X main.version=${VERSION}" -o "${BINARY}" ./cmd/guanaco; then
    ok "Successfully compiled ${BINARY}"
else
    die "Compilation failed"
fi

info "Installing binary to $INSTALL_DIR..."
mkdir -p "$INSTALL_DIR"
cp "${BINARY}" "$INSTALL_DIR/${BINARY}"
chmod +x "$INSTALL_DIR/${BINARY}"

ICON_PATH="utilities-terminal"
if [ -f "assets/guanaco.png" ]; then
    info "Installing custom icon..."
    mkdir -p "$ICON_DIR"
    cp "assets/guanaco.png" "$ICON_DIR/${BINARY}.png"
    ICON_PATH="$ICON_DIR/${BINARY}.png"
    gtk-update-icon-cache -f -t ~/.local/share/icons/hicolor 2>/dev/null || true
fi

info "Configuring desktop shortcut..."
mkdir -p "$DESKTOP_DIR"
cat << EOF > "$DESKTOP_DIR/${BINARY}.desktop"
[Desktop Entry]
Name=Guanaco
Comment=Private chat UI for local and cloud AI models
Exec="$INSTALL_DIR/${BINARY}"
Icon=$ICON_PATH
Terminal=false
Type=Application
Categories=Utility;Chat;
StartupNotify=true
EOF

update-desktop-database "$DESKTOP_DIR" 2>/dev/null || true

section "Setup Complete"
ok "Guanaco is ready and installed in your app menu!"