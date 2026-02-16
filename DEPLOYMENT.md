# Deployment Guide

## Version Format

Releases use date-based versioning:

```
YYYY.MM.DD.ID
```

- `YYYY` - Year (e.g., 2026)
- `MM` - Month (01-12)
- `DD` - Day (01-31)
- `ID` - Daily release counter starting from 0

Examples:
- `2026.02.16.0` - First release on Feb 16, 2026
- `2026.02.16.1` - Second release on the same day
- `2026.02.17.0` - First release on Feb 17, 2026

## Build & Release Process

### 1. Build Distribution Packages

```bash
make dist
```

This creates:
- `bin/klaw-darwin-amd64` + `.tar.gz`
- `bin/klaw-darwin-arm64` + `.tar.gz`
- `bin/klaw-linux-amd64` + `.tar.gz`
- `bin/klaw-linux-arm64` + `.tar.gz`
- `bin/klaw-windows-amd64.exe` + `.zip`
- `bin/checksums.txt`

### 2. Create GitHub Release

```bash
gh release create YYYY.MM.DD.ID \
  --title "YYYY.MM.DD.ID - Release Title" \
  --notes "Release notes here" \
  bin/klaw-darwin-amd64 \
  bin/klaw-darwin-arm64 \
  bin/klaw-linux-amd64 \
  bin/klaw-linux-arm64 \
  bin/klaw-windows-amd64.exe \
  bin/checksums.txt
```

### 3. Update Install Scripts

Update fallback version in both locations:

1. **klaw repo**: `install.sh` (then git push)
2. **website repo**: `public/install.sh` (then wrangler deploy)

Change the fallback version:
```sh
VERSION="YYYY.MM.DD.ID"
```

### 4. Deploy Website

```bash
cd ../website
wrangler deploy
```

## File Locations

| File | Purpose |
|------|---------|
| `Makefile` | Build targets (build, cross, release, dist) |
| `install.sh` | Installer script (in repo) |
| `bin/` | Build output directory |
| `bin/checksums.txt` | SHA256 checksums for all binaries |

## Related Repositories

- **klaw**: https://github.com/klawsh/klaw.sh
- **website**: Contains `public/install.sh` for https://klaw.sh
  - Deployed via Cloudflare Workers: `wrangler deploy`
  - No git push needed, just run wrangler

## Quick Deploy Commands

```bash
# Full release process
make dist
gh release create $(date +%Y.%m.%d).0 \
  --title "$(date +%Y.%m.%d).0 - Title" \
  --notes "Notes" \
  bin/klaw-darwin-amd64 \
  bin/klaw-darwin-arm64 \
  bin/klaw-linux-amd64 \
  bin/klaw-linux-arm64 \
  bin/klaw-windows-amd64.exe \
  bin/checksums.txt
```
