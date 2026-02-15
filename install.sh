#!/bin/sh
# klaw installer
# Usage: curl -fsSL https://klaw.sh | sh
#    or: curl -fsSL https://klaw.sh/install.sh | sh

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Config
GITHUB_REPO="klawsh/klaw.sh"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="klaw"

echo ""
echo "${BLUE}╭─────────────────────────────────────╮${NC}"
echo "${BLUE}│${NC}            ${GREEN}klaw installer${NC}            ${BLUE}│${NC}"
echo "${BLUE}│${NC}     Kubernetes for AI Agents      ${BLUE}│${NC}"
echo "${BLUE}╰─────────────────────────────────────╯${NC}"
echo ""

# Detect OS
detect_os() {
    OS="$(uname -s)"
    case "$OS" in
        Linux*)     OS="linux";;
        Darwin*)    OS="darwin";;
        MINGW*|MSYS*|CYGWIN*) OS="windows";;
        *)          echo "${RED}Unsupported OS: $OS${NC}"; exit 1;;
    esac
    echo "$OS"
}

# Detect architecture
detect_arch() {
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)   ARCH="amd64";;
        arm64|aarch64)  ARCH="arm64";;
        armv7l)         ARCH="arm";;
        *)              echo "${RED}Unsupported architecture: $ARCH${NC}"; exit 1;;
    esac
    echo "$ARCH"
}

# Get latest version
get_latest_version() {
    curl -sL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" | \
        grep '"tag_name":' | \
        sed -E 's/.*"([^"]+)".*/\1/' || echo "v0.1.0"
}

# Main install
main() {
    OS=$(detect_os)
    ARCH=$(detect_arch)

    echo "${YELLOW}Detected:${NC} $OS/$ARCH"

    # Get version
    VERSION=$(get_latest_version)
    echo "${YELLOW}Version:${NC} $VERSION"

    # Build download URL
    if [ "$OS" = "windows" ]; then
        FILENAME="klaw-${OS}-${ARCH}.exe"
    else
        FILENAME="klaw-${OS}-${ARCH}"
    fi

    DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/${FILENAME}"

    # Alternative: direct from klaw.sh
    ALT_URL="https://klaw.sh/releases/${VERSION}/${FILENAME}"

    echo "${YELLOW}Downloading:${NC} $FILENAME"

    # Create temp directory
    TMP_DIR=$(mktemp -d)
    TMP_FILE="${TMP_DIR}/${BINARY_NAME}"

    # Try GitHub first, then klaw.sh
    if curl -fsSL "$DOWNLOAD_URL" -o "$TMP_FILE" 2>/dev/null; then
        echo "${GREEN}Downloaded from GitHub${NC}"
    elif curl -fsSL "$ALT_URL" -o "$TMP_FILE" 2>/dev/null; then
        echo "${GREEN}Downloaded from klaw.sh${NC}"
    else
        echo "${RED}Failed to download klaw${NC}"
        echo ""
        echo "You can build from source:"
        echo "  git clone https://github.com/${GITHUB_REPO}.git"
        echo "  cd klaw && go build -o klaw ./cmd/klaw"
        rm -rf "$TMP_DIR"
        exit 1
    fi

    # Make executable
    chmod +x "$TMP_FILE"

    # Install
    echo "${YELLOW}Installing to:${NC} ${INSTALL_DIR}/${BINARY_NAME}"

    if [ -w "$INSTALL_DIR" ]; then
        mv "$TMP_FILE" "${INSTALL_DIR}/${BINARY_NAME}"
    else
        echo "${YELLOW}Need sudo to install to ${INSTALL_DIR}${NC}"
        sudo mv "$TMP_FILE" "${INSTALL_DIR}/${BINARY_NAME}"
    fi

    # Cleanup
    rm -rf "$TMP_DIR"

    # Verify installation
    if command -v klaw >/dev/null 2>&1; then
        echo ""
        echo "${GREEN}✓ klaw installed successfully!${NC}"
        echo ""
        klaw version 2>/dev/null || echo "  ${BLUE}klaw${NC} is ready"
    else
        echo ""
        echo "${GREEN}✓ klaw installed to ${INSTALL_DIR}/${BINARY_NAME}${NC}"
        echo ""
        echo "Add to your PATH if needed:"
        echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    fi

    echo ""
    echo "${BLUE}Get started:${NC}"
    echo ""
    echo "  ${YELLOW}# Set your API key${NC}"
    echo "  export EACHLABS_API_KEY=your-key    # or ANTHROPIC_API_KEY"
    echo ""
    echo "  ${YELLOW}# Start chatting${NC}"
    echo "  klaw chat"
    echo ""
    echo "  ${YELLOW}# Or run Slack bot${NC}"
    echo "  klaw slack"
    echo ""
    echo "${BLUE}Documentation:${NC} https://klaw.sh/docs"
    echo "${BLUE}GitHub:${NC}        https://github.com/${GITHUB_REPO}"
    echo ""
}

main "$@"
