# MiniMedia

A small home media server for Windows, macOS and Linux, and a Google TV app.
Point the server at your movie and TV folders; the TV app finds it on your
network and plays everything with a Netflix-style player: seek thumbnails,
Skip Intro, next episode, audio and subtitle choices remembered per show, and
"Ends at" times.

- The server only **reads** your media folders. It never renames, moves,
  writes or deletes anything in them.
- Video is never re-encoded. Audio your TV can't play (DTS, TrueHD, or Dolby
  Digital Plus on a TV that only takes AC3) is converted on the fly while the
  video is copied as-is, so it's fast and the picture is untouched.
- Optional TMDB metadata: posters, backdrops, logos, descriptions, in your
  language.

`PLAN.md` is the full specification.

---

## 1. Install the server

The server runs on Windows 10/11, macOS (Apple silicon or Intel) and 64-bit
Linux (Intel/AMD or ARM, e.g. a Raspberry Pi 4 or 5). The steps are the same
everywhere; where they differ, each system has its own note.

### What you need
**ffmpeg 5.1 or newer**, including ffprobe. MiniMedia looks for them next to
the `minimedia` program, then at the paths in the config, then on `PATH`, and
tells you at startup if the version is too old.

- **Windows:** `winget install Gyan.FFmpeg`, or download
  `ffmpeg-release-full.7z` from <https://www.gyan.dev/ffmpeg/builds/> and put
  `ffmpeg.exe` and `ffprobe.exe` next to `minimedia.exe`. Use a "full" build:
  it includes the HDR tone-mapping filter used for thumbnails.
- **macOS:** `brew install ffmpeg` (with [Homebrew](https://brew.sh)). That
  build has no HDR tone-mapping filter (zscale), so seek thumbnails of HDR
  films look washed out; playback is unaffected. For tone-mapped thumbnails,
  install an ffmpeg built with zimg instead, e.g. from the
  [homebrew-ffmpeg tap](https://github.com/homebrew-ffmpeg/homebrew-ffmpeg):
  `brew install homebrew-ffmpeg/ffmpeg/ffmpeg --with-zimg`.
- **Linux:** your distribution's package: `sudo apt install ffmpeg` on Debian
  12+, Ubuntu 24.04+ and Raspberry Pi OS (Bookworm); `sudo dnf install ffmpeg`
  on Fedora (from RPM Fusion). Older releases such as Ubuntu 22.04 ship ffmpeg
  4.4, which is too old; use a static build from
  <https://johnvansickle.com/ffmpeg/> there.

### Get the server
Download it from the [latest release](https://github.com/acip/minimedia/releases/latest):

| Computer | File |
|---|---|
| Windows 10/11 | `minimedia-windows-amd64.exe` (rename it to `minimedia.exe`) |
| Mac with Apple silicon (M1 and later) | `minimedia-macos-arm64.tar.gz` |
| Intel Mac | `minimedia-macos-amd64.tar.gz` |
| Linux PC or server | `minimedia-linux-amd64.tar.gz` |
| Raspberry Pi 4/5 or other 64-bit ARM Linux | `minimedia-linux-arm64.tar.gz` |

Unpack the macOS and Linux files with `tar -xzf minimedia-*.tar.gz`. The
programs aren't signed with a paid certificate, so on first run Windows may
say "Windows protected your PC" (click **More info**, then **Run anyway**),
and macOS may refuse to open it (run `xattr -d com.apple.quarantine minimedia`
in its folder).

Or build it yourself with Go 1.27+. Building the TV app as well needs Android
Studio's JDK and SDK.

- **Windows:**

  ```powershell
  .\build.ps1            # servers for every system, plus dist\MiniMedia-tv.apk
  .\build.ps1 -SkipApp   # just the servers
  ```

  This makes `dist\minimedia.exe` and also `minimedia-macos-arm64` (Apple
  silicon), `minimedia-macos-amd64` (Intel Macs), `minimedia-linux-amd64` and
  `minimedia-linux-arm64`, so one Windows PC can build for every computer in the house.
- **macOS and Linux:**

  ```sh
  ./build.sh             # dist/minimedia for this computer, plus dist/MiniMedia-tv.apk
  ./build.sh --skip-app  # just the server
  ```

Put the program in a folder of its own, e.g. `C:\MiniMedia` or `~/MiniMedia`,
and on macOS and Linux name it `minimedia`. If you copied a build from another
computer, make it runnable with `chmod +x minimedia`.

### First run
1. Run it: `minimedia.exe` on Windows, `./minimedia` in a terminal on macOS
   and Linux. With no config it writes a default `config.yaml` and tells you
   where: next to the program if that folder is writable, otherwise
   - Windows: `%ProgramData%\MiniMedia\config.yaml`
   - macOS: `~/Library/Application Support/MiniMedia/config.yaml`
   - Linux: `~/.config/minimedia/config.yaml` (`/etc/minimedia/config.yaml`
     when run as root)
2. Edit `config.yaml`: add your folders under `libraries`. Every setting is
   explained in the file. The minimum is:

   ```yaml
   libraries:
     - name: Movies
       type: movies
       path: 'D:\Media\Movies'          # macOS: /Volumes/Media/Movies, Linux: /mnt/media/Movies
     - name: Shows
       type: shows
       path: '\\nas\media\Shows'        # a network share (Windows UNC path)
   tmdb_api_key: "your key"        # optional, free at themoviedb.org → Settings → API
   metadata_language: ro-RO        # optional
   subtitles:
     preferred_languages: [ro, en] # subtitle and audio languages, in order
   ```

   On Windows, backslash paths need single quotes. On macOS and Linux, mount a
   network share first (Finder → Go → Connect to Server, or `/etc/fstab`) and
   use its folder. Typos in setting names are reported at startup with the
   line number.
3. Run it again. It scans the folders (new files are picked up while it runs),
   then fetches metadata and builds seek thumbnails in the background.

The server takes no command-line arguments; everything is in `config.yaml`.
Config changes need a restart; media changes don't.

### Allow it through the firewall
The TV connects on TCP port 8096 (the `port` setting) and finds the server
through UDP port 7360.

- **Windows:** the first time it runs, Windows asks whether to allow MiniMedia
  on networks. Tick **Private networks** and allow. If you missed the prompt,
  run this in an administrator PowerShell (adjust the path):

  ```powershell
  New-NetFirewallRule -DisplayName "MiniMedia" -Direction Inbound -Program "C:\MiniMedia\minimedia.exe" -Action Allow -Profile Private
  ```

  Your home network must be set to **Private** (Settings → Network & internet
  → your Wi-Fi or Ethernet → Network profile type). On a **Public** network
  the firewall blocks the TV, and MiniMedia logs a warning about it at startup.
- **macOS:** if the firewall is on (System Settings → Network → Firewall),
  macOS asks whether `minimedia` may accept incoming connections: choose
  **Allow**. A rebuilt program counts as a new one, so it may ask again after
  an update. If macOS asks for **Local Network** access, allow that too.
- **Linux:** with ufw, `sudo ufw allow 8096/tcp && sudo ufw allow 7360/udp`;
  with firewalld,
  `sudo firewall-cmd --permanent --add-port=8096/tcp --add-port=7360/udp && sudo firewall-cmd --reload`.

### Start it automatically (optional)
- **Windows:** MiniMedia is a normal program, not a Windows service. To start
  it when you log in, put a shortcut to `minimedia.exe` in your Startup folder
  (press Win+R, type `shell:startup`). In the shortcut's properties you can set
  **Run: Minimized**. It keeps running while you're logged in, including when
  the screen is locked.
- **macOS:** save this as `~/Library/LaunchAgents/minimedia.plist` (adjust the
  path; launchd needs the full path, not `~`), then run
  `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/minimedia.plist`.
  It starts when you log in.

  ```xml
  <?xml version="1.0" encoding="UTF-8"?>
  <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
  <plist version="1.0">
  <dict>
    <key>Label</key><string>minimedia</string>
    <key>ProgramArguments</key><array><string>/Users/you/MiniMedia/minimedia</string></array>
    <key>RunAtLoad</key><true/>
    <key>StandardErrorPath</key><string>/Users/you/MiniMedia/minimedia.log</string>
  </dict>
  </plist>
  ```
- **Linux:** save this as `~/.config/systemd/user/minimedia.service` (adjust
  the path; `%h` is your home folder):

  ```ini
  [Unit]
  Description=MiniMedia
  After=network-online.target

  [Service]
  ExecStart=%h/MiniMedia/minimedia
  Restart=on-failure

  [Install]
  WantedBy=default.target
  ```

  Then `systemctl --user enable --now minimedia`. To start it at boot rather
  than at login (for a computer nobody logs in to), also run
  `sudo loginctl enable-linger $USER`. `journalctl --user -u minimedia` shows
  its log.

### While it runs
- The computer won't go to sleep while the TV is playing (from the first
  stream request until 2 minutes after the last). The display may still turn
  off. On Linux this needs a desktop session; a headless server can't block
  sleep (MiniMedia logs this once), so set it not to sleep.
- ffmpeg processes it starts are stopped when MiniMedia exits. On Windows and
  Linux that holds even if it is killed or crashes; on macOS a running ffmpeg
  finishes its current job first.
- New files show up within seconds. On macOS, where MiniMedia checks the
  folders every 30 seconds instead of being notified, it takes up to half a
  minute.
- Close it with Ctrl+C (or by closing the window).
- Set `log_file` in `config.yaml` to also keep a log file. It's rotated at
  10 MB, with 3 old files kept.

---

## 2. Install the TV app (Google TV / Android TV)

The app isn't on the Play Store. Download `MiniMedia-tv.apk` from the
[latest release](https://github.com/acip/minimedia/releases/latest) (or build
it, section 1) and install it once with Android's `adb` tool (part of Android
Studio, or "SDK Platform Tools" from
<https://developer.android.com/tools/releases/platform-tools>). The TV needs
Google TV or Android TV 12 or newer.

1. On the TV: **Settings → System → About**, scroll to **Android TV OS build**
   and press OK seven times until it says you are a developer.
2. **Settings → System → Developer options**: turn on **USB debugging**, or on
   newer devices (Chromecast with Google TV) **Wireless debugging**.
3. On the computer, connect:
   - Wireless debugging: open **Wireless debugging → Pair device with pairing
     code** on the TV, then

     ```sh
     adb pair 192.168.1.50:41234 123456   # address and code shown on the TV
     adb connect 192.168.1.50:37000       # the address on the Wireless debugging screen
     ```

     (`adb mdns services` lists both addresses while the TV shows them.)
   - USB debugging (older devices): `adb connect 192.168.1.50:5555` and accept
     the prompt on the TV.
4. Install (and later update) the app, open it once, then have Android compile
   it ahead of time. Otherwise the TV does that on its own some time later
   (usually overnight), and until then the menus are less smooth.

   ```sh
   adb install -r MiniMedia-tv.apk
   adb shell am start -n io.github.acip.minimedia/.MainActivity
   # wait about 10 seconds, then:
   adb shell cmd package compile -m speed-profile -f io.github.acip.minimedia
   ```

Open **MiniMedia** from the TV's apps. It finds the server automatically. If
it doesn't (see Troubleshooting), enter the computer's address and port,
e.g. `192.168.1.20:8096`.

**Frame rate:** for smooth 24 fps films, turn on the TV's **Match content
frame rate** (Settings → Display & Sound → Advanced display settings). The app
leaves refresh-rate switching to that setting.

---

## 3. Organising your media

### Movies (`type: movies`)
- `Movies\Title (Year)\Title (Year).mkv`, or loose `Title (Year).mkv`, or
  scene names like `Title.2019.1080p.BluRay.x264-GRP.mkv`.
- A movies folder may also contain **TV episodes**, as download folders often
  do: files named with `S01E02` or `1x02` become episodes, grouped into shows by
  the name before the code or by their folder (`Show (2025)\Season 2\…`,
  `Show.S01.1080p.WEB\…`).

### Shows (`type: shows`)
- `Shows\Show Name (Year)\Season 01\Show Name - S01E02 - Title.mkv`.
- `Specials` (or `Season 00`) is season 0. Multi-episode files: `S01E02E03` or
  `S01E02-E03`. A file named only `E05` takes the season from its folder.

Files that can't be parsed are skipped and listed at
`http://<pc>:8096/api/library/unmatched`. Folders named `Extras`, `Trailers`,
`Featurettes`, `Behind The Scenes`, `Deleted Scenes`, `Interviews`, `Samples`
or `Other`, and small `sample` files, are ignored.

### Subtitles
Next to the video, or in a `Subs`/`Subtitles` folder:
`Movie.srt`, `Movie.ro.srt`, `Movie.en.forced.srt`, `Movie.en.sdh.srt`, and
`Subs\Movie\2_English.srt`. `.ass`, `.ssa` and `.vtt` work too. Romanian files
in old encodings (Windows-1250, ISO-8859-2) are converted, and cedilla letters
(ş ţ) become the correct comma-below ones (ș ț).

### Artwork and metadata
- In a movie or show folder: `poster.jpg` / `folder.jpg` / `cover.jpg`,
  `fanart.jpg` / `backdrop.jpg`, `logo.png` / `clearlogo.png`. For movies
  sharing a folder: `<file name>-poster.jpg`. Episode stills:
  `<episode file name>-thumb.jpg`.
- Local pictures win over TMDB; without either, backdrops and stills are
  frames from the video.
- Wrong TMDB match? Put a `tmdb.txt` in the movie or show folder containing
  the TMDB id or URL.

---

## 4. Troubleshooting

**The TV app doesn't find the server.** Check that the firewall allows
MiniMedia and, on Windows, that the network is **Private** (section 1).
Discovery uses UDP port 7360, and the app connects on the configured `port`
(8096 by default). If another program already uses that port, MiniMedia exits
with an error; pick another `port`. A VPN on the computer is fine; on the TV,
enter the address manually if discovery still fails (`ipconfig` on Windows,
`ipconfig getifaddr en0` on macOS or `hostname -I` on Linux shows it).

**"ffmpeg … is version 4.4, but MiniMedia needs 5.1 or newer".** Install a
newer ffmpeg (section 1), or point `ffmpeg` and `ffprobe` in `config.yaml` at
one.

**No sound, or "converted for this device".** The TV can't play that audio
format itself, so the server converts it: to E-AC3 5.1, AC3 5.1 or AAC stereo,
whichever the TV supports. That's expected and keeps surround where possible.
If a TV with a soundbar reports less than it supports, check the TV's
**Settings → Display & Sound → Audio → Surround sound**.

**The first play of a big file takes a few seconds.** Before converting audio
the server indexes the file's keyframes. This happens in the background after
a scan, so it's usually ready; otherwise it takes roughly as long as reading
the file once.

**A subtitle is "unavailable with converted audio".** Picture-based subtitles
(PGS, VobSub) can only be shown when the file plays directly; with converted
audio, choose a text subtitle or an external `.srt`.

**Wrong colours on a Dolby Vision (profile 5) file.** These need a Dolby
Vision display; the app warns about it before playing.

**A network share isn't scanned.** On Windows use the UNC path
(`\\nas\share\Movies`); on macOS and Linux use the folder where the share is
mounted. If the share is unreachable when a scan runs, MiniMedia keeps the
existing items instead of removing them. Changes on shares are picked up by
the periodic rescan (`scan.rescan_every_hours`), and on macOS within 30
seconds.

**Where are the logs and the database?** The log goes to the console window
(and to `log_file` if set). The database, thumbnails and caches are in
`data_dir`, by default:
- Windows: `%ProgramData%\MiniMedia\data`
- macOS: `~/Library/Application Support/MiniMedia/data`
- Linux: `~/.local/share/minimedia` (`/var/lib/minimedia` when run as root)

Deleting `data_dir` starts over (watched state and preferences included).
Converted audio is cached there too: at most 20 GB, and unused files go after
6 hours.

**Status at a glance:** `http://<computer>:8096/api/library/status` shows the
scan, metadata, thumbnail and audio-conversion progress.

---

## Development

Requirements: Go 1.27+, ffmpeg/ffprobe 5.1+ (full build) on PATH, and for the
app Android Studio (JDK 17, SDK 37). The helper scripts below are PowerShell
and written for Windows; on macOS and Linux, `go test ./...` and `./build.sh`
work as they are.

```powershell
.\testdata\generate.ps1          # make the sample library in testdata\media (about 20 s)
.\testdata\run-dev.ps1           # build testdata\run\minimedia.exe, write a dev config, run it
```

The dev config uses port **8098** so it can run alongside another media
server on 8096. Edit `testdata\run\config.yaml` to point it at other folders.

Platform-specific code lives in `_windows.go`, `_darwin.go`, `_linux.go` and
`_unix.go` files (folder watching, sleep, child processes, long paths). To
check every target from one machine:

```sh
cd server
for os in windows darwin linux; do GOOS=$os go vet ./...; done
```

```powershell
cd server
go test ./...                    # unit tests; several also use testdata\media when present
go run ./cmd/discover            # UDP discovery test client (broadcast)
..\testdata\discover.ps1         # the same in PowerShell
curl.exe http://localhost:8098/api/movies
curl.exe -r 0-999 -o part.bin http://localhost:8098/stream/<id>/file

cd ..\tv-app
.\gradlew.bat assembleDebug testDebugUnitTest
```

A debug build runs several times slower on a TV than the release build
(`assembleRelease`, what `build.ps1` ships), so measure UI speed on a release
build. On TV GPUs, avoid full-screen gradients and translucent full-screen
layers: on a Chromecast with Google TV, two of them cost ~40 ms per frame.
`ui/Scrims.kt` explains the alternatives used.

The app keeps actions instant by doing the work ahead of time. Screens stay
built under the one on top (`ui/ScreenStack.kt`), so Back only reveals them.
While focus rests on a card, its details and artwork are fetched
(`data/DetailsCache.kt`). On a details page, or a card that plays, the video
is buffered paused (`ui/player/Preload.kt`), so Play just starts it.

In the Android TV emulator, enter `10.0.2.2:8098` (UDP discovery replies don't
reach the emulator).

### Releases

Pushing a tag like `v0.2.0` runs `.github/workflows/release.yml`: the tests on
Windows, macOS and Linux, then the servers for every system and the TV app,
published as a GitHub Release. The tag sets the version of both (the app's
versionCode is `major×1000000 + minor×1000 + patch`). Every push to `main`
runs the same tests (`ci.yml`).

The app in releases is signed with the project's release key, kept in the
repository secrets `MINIMEDIA_KEYSTORE_BASE64` and
`MINIMEDIA_KEYSTORE_PASSWORD`. A local build uses that key too when
`tv-app/keystore.properties` exists (git-ignored):

```properties
storeFile=C:/path/to/minimedia-release.jks
storePassword=...
keyAlias=minimedia
keyPassword=...
```

Without it, release builds are signed with your debug key, and the TV refuses
to install them over a release (and the other way round) until the app is
uninstalled.

### Layout

```
server/      Go module: cmd/minimedia (server), cmd/discover (test client), internal/...
tv-app/      Android TV app (Kotlin, Compose for TV, Media3)
.github/     CI (tests on Windows, macOS, Linux) and the release workflow
testdata/    generate.ps1, run-dev.ps1, discover.ps1 (generated media is git-ignored)
build.ps1    release build into dist\ (Windows; also cross-builds the macOS and Linux servers)
build.sh     release build into dist/ (macOS and Linux)
```

## License

MIT. See `LICENSE`.
