## Downloads

**Server** (pick the one for the computer that has your media):

| Computer | File |
|---|---|
| Windows 10/11 | `minimedia-windows-amd64.exe` (rename it to `minimedia.exe`) |
| Mac with Apple silicon (M1 and later) | `minimedia-macos-arm64.tar.gz` |
| Intel Mac | `minimedia-macos-amd64.tar.gz` |
| Linux PC or server | `minimedia-linux-amd64.tar.gz` |
| Raspberry Pi 4/5 or other ARM Linux (64-bit) | `minimedia-linux-arm64.tar.gz` |

**TV app**: `MiniMedia-tv.apk`, for Google TV and Android TV 12 or newer.

## Install

1. Install **ffmpeg 5.1 or newer**: `winget install Gyan.FFmpeg` (Windows), `brew install ffmpeg` (macOS), or your distribution's `ffmpeg` package (Linux; Debian 12+, Ubuntu 24.04+).
2. Put the server in a folder of its own and run it. On macOS and Linux, unpack it first (`tar -xzf minimedia-*.tar.gz`) and run `./minimedia`. The first run writes a `config.yaml` and says where; add your media folders to it and run the server again.
3. Install the TV app with `adb install -r MiniMedia-tv.apk`. The [README](https://github.com/acip/minimedia#2-install-the-tv-app-google-tv--android-tv) shows how to enable this on the TV.

The [README](https://github.com/acip/minimedia#readme) covers each step in detail, including the firewall and starting the server automatically.

## Security warnings on first run

These programs aren't signed with a paid certificate, so:

- **Windows** may show "Windows protected your PC". Click **More info**, then **Run anyway**.
- **macOS** may refuse to open the server. Run `xattr -d com.apple.quarantine minimedia` in its folder, then start it again.

`SHA256SUMS.txt` lists the checksum of every file.

## Updating the TV app

Releases are signed with the MiniMedia release key, so later releases install over this one with `adb install -r`. If the TV has a build you made yourself before {{VERSION}}, uninstall it first (`adb uninstall io.github.acip.minimedia`); the app then finds the server again on its own.
